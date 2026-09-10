# ClipForge Web v3.3.1 —— API Key 本地加密改造计划

> 本文档写给一个「轻量模型」直接照着执行。每一步都给出：改哪个文件、改哪个函数、完整可粘贴的代码。做完即自检、可回滚。
> 目标：**消除 `settings.yml` 里 API Key 明文**，改用「本地主密钥 + AES-256-GCM」加密落盘，**不依赖任何系统钥匙串工具**（无 macOS Keychain / Windows DPAPI / Linux Secret Service），Windows / Linux / macOS / Android 四端同一套 Go 代码通用。

---

## 0. 现状（为什么需要改）

| 项 | 现状 | 位置 |
|---|---|---|
| 配置文件 | `settings.yml`，**API Key 明文** | `os.UserConfigDir()/ClipForge/settings.yml` |
| 写入 | `# ClipForge 设置(明文...)\napiKey: %q` | `server/main.go` `saveConfig()` 约 L85–92 |
| 读取 | 逐行解析 `apiKey:` 后面的明文 | `server/main.go` `loadConfig()` 约 L67–83 |
| 密钥消费 | 启动时 `apiKey = cfg.APIKey`，POST 时 `setAPIKey` | `main()` L515–516、`/api/config` L524–546 |
| 前端 | POST `{apiKey}` 存 / GET 返回 `{configured:bool}`（不回传 key） | `static/app.js` L296–319 |

**关键结论**：加密只影响后端 `loadConfig`/`saveConfig` 两个函数；`getAPIKey()`、`dashScopeProxy()`、前端、CLI（`cli.go` 走 `/api/config`）全部不用改。

---

## 1. 目标与非目标

**目标**
1. `settings.yml` 里不再出现 API Key 明文。
2. 加密密钥本地生成、本地保存，不调用任何系统钥匙串/钥匙环工具。
3. 四端（Win/Linux/macOS/Android）同一份代码，不新增第三方依赖（纯 Go 标准库）。
4. 老用户旧明文 `settings.yml` **自动迁移**到加密格式，无需手动操作。

**非目标（本次不做）**
- 不做「用户口令派生密钥」的交互流程（列为 §9 可选项）。
- 不做跨设备不可解密的「硬件绑定」（列为 §9 可选项）。
- 不加密 `bill.jsonl`（账本无敏感凭证）。

---

## 2. 方案设计

采用**两文件分离 + AES-256-GCM**：

- **主密钥（KEK）**：32 字节随机数，`crypto/rand` 生成，存 `master.key`（权限 `0600`），与 `settings.yml` 同目录。
- **数据密钥（API Key）**：用主密钥做 AES-256-GCM 加密，密文写回 `settings.yml`。

磁盘格式（`settings.yml` 从 v1 明文改为 v2 密文信封）：

```
version: 2
apiKeyEnc: "<nonce_hex>:<ciphertext_base64>"
```

`master.key` 内容是 32 字节原始随机字节（不是文本），`0600` 权限，与密文**分文件存放**。

**安全边界（如实说明）**：不用系统钥匙串、也不用口令时，本地拿到 `master.key` 且能读取密文的攻击者仍可解密。本方案的收益是：① key 不再明文躺在文件里；② 必须同时拿到两个文件才能解；③ 文件级 `0600` 限制非本人读取；④ 单独泄漏/备份 `settings.yml` 一文无用。属于「纵深防御/提升门槛」，不能替代钥匙串在强威胁模型下的作用。

---

## 3. 实施步骤

### 步骤 1：新增 `server/secret.go`

新建文件，路径 `clipforge-web/server/secret.go`，内容如下（纯标准库，无新依赖）：

```go
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const masterKeyFile = "master.key" // 与 settings.yml 同目录, 0600 权限

// masterKeyPath 返回主密钥文件绝对路径（与 configPath 同目录）。
func masterKeyPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, appDir, masterKeyFile)
}

// ensureMasterKey 返回 32 字节主密钥；不存在则用 crypto/rand 生成并 0600 落盘。
func ensureMasterKey() ([]byte, error) {
	p := masterKeyPath()
	if b, err := os.ReadFile(p); err == nil && len(b) == 32 {
		return b, nil
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, key, 0600); err != nil {
		return nil, err
	}
	return key, nil
}

// encryptSecret 用 32 字节 key 对 plain 做 AES-256-GCM，返回 "<nonce_hex>:<ct_base64>"。
func encryptSecret(plain string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize()) // 12 字节
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plain), nil)
	return hex.EncodeToString(nonce) + ":" + base64.StdEncoding.EncodeToString(ct), nil
}

// decryptSecret 逆操作，失败（主密钥丢失/更换）返回错误。
func decryptSecret(blob string, key []byte) (string, error) {
	i := strings.IndexByte(blob, ':')
	if i < 0 {
		return "", errors.New("bad encrypted blob")
	}
	nonce, err := hex.DecodeString(blob[:i])
	if err != nil {
		return "", err
	}
	ct, err := base64.StdEncoding.DecodeString(blob[i+1:])
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", errors.New("decrypt failed: master key missing or changed")
	}
	return string(pt), nil
}
```

---

### 步骤 2：改 `server/main.go` 的 `loadConfig`

把原来的 `loadConfig`（约 L67–83）**整体替换**为下面版本（含 v1→v2 自动迁移）：

```go
func loadConfig() Config {
	var c Config
	data, err := os.ReadFile(configPath())
	if err != nil {
		return c
	}
	s := string(data)

	// v2 密文格式：version: 2 + apiKeyEnc
	if strings.Contains(s, "apiKeyEnc:") {
		key, err := ensureMasterKey()
		if err != nil {
			return c
		}
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "apiKeyEnc:") {
				v := strings.TrimSpace(strings.TrimPrefix(line, "apiKeyEnc:"))
				v = strings.Trim(v, `"'`)
				if v == "" {
					continue
				}
				if pt, err := decryptSecret(v, key); err == nil {
					c.APIKey = pt
				}
			}
		}
		return c
	}

	// v1 明文（旧版）：读出来，稍后自动迁移加密
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "apiKey:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "apiKey:"))
			c.APIKey = strings.Trim(v, `"'`)
		}
	}
	// 一次迁移：非空即加密重写（内部会生成 master.key）
	if c.APIKey != "" {
		_ = saveConfig(c)
	}
	return c
}
```

---

### 步骤 3：改 `server/main.go` 的 `saveConfig`

把原来的 `saveConfig`（约 L85–92）**整体替换**为：

```go
func saveConfig(c Config) error {
	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// 清除 key：写空 v2 文件（不生成 master.key）
	if c.APIKey == "" {
		return os.WriteFile(configPath(), []byte("version: 2\napiKeyEnc: \"\"\n"), 0600)
	}
	key, err := ensureMasterKey()
	if err != nil {
		return err
	}
	enc, err := encryptSecret(c.APIKey, key)
	if err != nil {
		return err
	}
	content := fmt.Sprintf("version: 2\napiKeyEnc: %q\n", enc)
	return os.WriteFile(configPath(), []byte(content), 0600)
}
```

> `fmt`、`os`、`filepath`、`strings` 在 `main.go` 已 import，无需新增 import。`secret.go` 复用 `appDir`、`configPath()` 这两个已存在于 `main.go` 的标识符（同 package）。

---

### 步骤 4：检查是否有其它明文写入点

用下面的命令全局扫一遍，确认除 `saveConfig` 外没有别处把 API Key 明文写盘：

```bash
cd clipforge-web
grep -rn "apiKey:" server/*.go        # 应只剩 loadConfig 迁移解析旧格式 + 注释
grep -rn "apiKeyEnc\|master.key\|encryptSecret\|decryptSecret" server/*.go
grep -rni "WriteFile\|os.Create" server/*.go   # 确认没有把 key 写到别的文件
```

预期：`apiKey:` 仅出现在 `loadConfig` 的 v1 迁移分支；新的加密函数只在 `loadConfig`/`saveConfig` 被调用。

---

### 步骤 5：编译自检（四端均要能编过）

```bash
cd clipforge-web/server

# 本机 Linux/macOS 快速编一次
go build ./...

# Windows（无 CGO，单文件 exe）
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/cf-win.exe .

# Linux
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/cf-linux .

# Android（沿用现有 android/arm64 交叉编译方式，见 build.sh）
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -buildmode=c-shared -o /tmp/libclipforge.so .
```

> 若 Android 交叉编译需 `-buildmode=c-shared` 之外的既有参数，照 `clipforge-android/build.sh` 里那条 Go 命令原样替换输出即可，本步骤只验证「新代码能编译通过」。

---

## 4. 迁移与运行验证（手工）

1. 造一个旧明文文件，模拟老用户：
   ```bash
   mkdir -p "$(go env GOPATH 2>/dev/null)" 2>/dev/null
   # 直接写入应用实际 configPath（见启动日志打印的「配置文件: ...」路径）
   # 示例（macOS）：~/Library/Application Support/ClipForge/settings.yml
   # 内容写：  apiKey: "sk-fake123"
   ```
2. 启动程序，观察：
   - 启动日志打印配置文件路径。
   - `settings.yml` 已被改写为 `version: 2` + `apiKeyEnc: "..."`，**内容里搜不到 `sk-fake123`**。
   - 同目录出现 `master.key`（32 字节），权限 `0600`。
3. 功能回验（确保解密正确、请求能通）：
   - 打开 `http://127.0.0.1:8731` 设置页，`GET /api/config` 返回 `{"configured":true}`。
   - 走一次「文生图」或「试试手气 Pro」，能正常返回（说明内存里的 key 正确）。
4. 清除回验：设置页清空 key → `settings.yml` 变 `version: 2` + 空 `apiKeyEnc`，`GET /api/config` 返回 `configured:false`。
5. 主密钥丢失回验：删掉 `master.key`（先备份），重启 → 解不出 key → 表现为「未设置 API Key」，提示重新填入即可，程序不崩溃。

---

## 5. 四端注意点

- **Windows / Linux / macOS**：`os.UserConfigDir()` 分别落在 `%AppData%`、`~/.config`、`~/Library/Application Support`，`master.key` 与 `settings.yml` 同目录，权限 `0600`，无钥匙串调用。
- **Android**：`os.UserConfigDir()` 通常解析失败回退 `"."`，即 Go 二进制的工作目录（app 私有 data 目录），本身已应用私有隔离；`master.key`/`settings.yml` 落在 app 私有目录，安全性与旧明文同等级但不再是明文。**请确认** MainActivity 启动二进制时的工作目录就是 app 私有目录（而非外置共享存储），如不是，需在启动参数里指定 `HOME`/工作目录（`server/diag.go` 可加一条返回 configPath 便于验证）。

---

## 6. 验收标准

- [ ] `settings.yml` 全文不含 API Key 明文（`grep` 无 `sk-`）。
- [ ] `master.key` 存在、32 字节、权限 `0600`。
- [ ] 旧明文文件首次启动被自动迁移，功能不中断。
- [ ] 设置/清除/读取三路径正常，`/api/config` GET 仍只返回 `configured`。
- [ ] 四端 `go build` 均通过，无新增第三方依赖（`go.mod` 未变）。
- [ ] `master.key` 删除后程序不崩，提示重新设置。

---

## 7. 回滚

若上线后发现问题，回退只需两点：
1. `git revert` 掉本次 `server/secret.go` + `main.go` 两个文件的改动。
2. 用户侧旧明文仍在时可直接用；若已迁移，把 `settings.yml` 里 `apiKeyEnc` 密文解出（本地仍可解）或让用户重填一次 key。

> `master.key` 是加密的关键，**绝不能提交进 git**（已建议加入 `.gitignore`，见 §8）。

---

## 8. 必须同步改的 `.gitignore`

在 `clipforge-web/.gitignore`（若无则新建）追加：

```
# 本地密钥（含主密钥与密文，绝不入库）
master.key
settings.yml
```

> 目前 `settings.yml` 在用户配置目录（不在仓库内），但 Android 本地构建/调试时工作目录可能就在仓库附近，务必加上，防止误提交。

---

## 9. 可选增强（本次不做，留档）

1. **口令派生密钥（无 `master.key` 文件）**：用用户口令经 PBKDF2-HMAC-SHA256（约 20 万次迭代）派生 32 字节主密钥。需引入 `golang.org/x/crypto/pbkdf2`（单文件、可离线 vendor），并在设置页增加「主口令」输入框，启动时要求输入。优点：本地无任何密钥文件；缺点：每次启动要输口令。
2. **硬件绑定**：把稳定设备标识混入主密钥材料（Windows `MachineGuid` 注册表、macOS `IOPlatformUUID`、Linux `/etc/machine-id`），使复制 `master.key`+密文到别的机器无法解密。缺点：重新引入各系统差异化代码。
