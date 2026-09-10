# ClipForge Web (Go + HTML5)

> **v3.2.2** —— 基于阿里云百炼（DashScope）的本地 AI 客户端，支持视频生成、文生图、语音合成（TTS）、声音克隆与生成账本。
> 单文件 Go 程序，**零运行时依赖、纯静态编译**；前端（HTML/CSS/JS）已用 `go:embed` 内嵌进二进制，无需外部目录。
> **面向 Windows（WebView2 内嵌窗口）与 Linux（CLI + 浏览器）分发**。macOS 请用原生 SwiftUI 版（仓库 [videogenerator](https://github.com/PlayChessClub/videogenerator) 的 `build.sh` 产出 `ClipForge.app`）；本 Web 版在 darwin 上**仅作本地开发调试**，非官方支持平台。
> 三条平行发布线：**本仓库**（Web：Windows / Linux）· [videogenerator](https://github.com/PlayChessClub/videogenerator)（macOS 原生，主力）· [ClipForge-ios](https://github.com/PlayChessClub/ClipForge-ios)（iOS / iPadOS）。

---

## 1. 它能做什么

| 模块 | 能力 | 后端模型 / 接口 |
|---|---|---|
| 声音工坊（克隆） | 上传 3–10s 清晰人声 → 克隆出 `voice_id` | `voice-enrollment` |
| 语音合成 | 选音色 + 调节语速/音量/音调，WebSocket 全双工合成，导出 mp3 | `cosyvoice-v3.5-plus` |
| 图片工坊 | 文生图，支持 4 个模型、3 种尺寸、1–4 张、智能扩写、🎲 试试手气 | `qwen-image-2.0(-pro)` / `wan2.7-image(-pro)` |
| 视频工坊 | 文生视频 / 图生视频，多模型、分辨率、时长、镜头、音频轨、AI 增强、🎲 试试手气 | `wan2.6/2.7-i2v(-flash)` / `wan2.6/2.7-t2v` |
| 账本 | 每次「确认生成」的预估明细落盘，今日/本月/累计汇总，CSV 导出 | 本地 `bill.jsonl` |
| 设置 | DashScope API Key 存本地，内置价目表 | —— |

> **为什么是「本地代理」模式**：API Key 只保存在你本机，所有 DashScope 请求由本地 Go 服务在后端注入 `Authorization` 后转发。前端/CLI 永远不直接接触 Key，也不上报任何密钥。

---

## 2. 平台支持

| 平台 | 运行形态 | 说明 |
|---|---|---|
| **Windows** | 单文件 `.exe` + **内嵌 WebView2 窗口**（Chromium 内核，关窗即退出） | `WebView2Loader.dll` 已内嵌，仍是单文件；若系统缺 WebView2 Runtime（极老精简系统）自动退回打开默认浏览器 |
| **Linux** | `.deb` 安装包 / `.zip`；**桌面图标启动 → 自动开浏览器**；**终端有 TTY → 命令行菜单** | 依赖 `xdg-utils`（自动开浏览器/下载目录）；纯静态编译无 CGO |
| **macOS** | 仅调试：运行后打开系统浏览器 `http://127.0.0.1:8731` | **非官方支持**，请用原生 `ClipForge.app` |

---

## 3. 快速开始（从源码运行）

需要 **Go 1.23+**：https://go.dev/dl/

```bash
git clone https://github.com/PlayChessClub/clipforge-web.git
cd clipforge-web/server
go run .
# Windows: 自动弹出内嵌 WebView2 窗口
# Linux 终端:   进入命令行菜单；桌面/无 TTY 启动: 自动开浏览器
# macOS:        仅开发调试,打开浏览器访问 127.0.0.1:8731
```

首次启动后在「设置」页填入 DashScope API Key 即可使用。

---

## 4. 构建各平台 Release

工程已 vendored 依赖，**离线可编译**。构建脚本 `build-pkgs.py` 一律 `CGO_ENABLED=0` 静态交叉编译，产物落在 `release/`。

```bash
# 默认: Windows amd64 + arm64 的 zip
python3 build-pkgs.py

# Linux 指定架构并打 deb(同时产出 zip)
python3 build-pkgs.py --only linux-amd64   --deb
python3 build-pkgs.py --only linux-arm64   --deb

# 任意单一目标
python3 build-pkgs.py --only windows-arm64
```

等价手写交叉编译（不依赖脚本）：

```bash
cd server
# Windows
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -o ../release/clipforge-windows-amd64.exe .
# Linux
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -mod=vendor -ldflags="-s -w" -o ../release/clipforge-linux-amd64   .
```

### Release 产物一览（v3.2.2）

| 产物 | 平台 | 形态 |
|---|---|---|
| `clipforge-windows-amd64.zip` | Windows x64 | 单文件 exe |
| `clipforge-windows-arm64.zip` | Windows ARM64 | 单文件 exe |
| `clipforge-linux-amd64.zip` | Linux x64 | 单文件二进制 |
| `clipforge-linux-arm64.zip` | Linux ARM64 | 单文件二进制 |
| `clipforge_3.2.2_amd64.deb` | Linux x64 (Debian/Ubuntu) | 含 `.desktop`、图标、版权 |
| `clipforge_3.2.2_arm64.deb` | Linux ARM64 (Debian/Ubuntu) | 含 `.desktop`、图标、版权 |

> CI（`.github/workflows/build-web.yml`）在打 `v*` tag 时自动对 Linux amd64/arm64 构建 zip + deb 并上传 artifact。

---

## 5. 使用方式

### 命令行（CLI，全平台通用）
Linux 终端直接运行（有 TTY）即进入交互菜单；其他平台用 `--cli` 强制：

```
clipforge --cli
```

菜单：**1 视频 · 2 文生图 · 3 语音合成 · 4 声音克隆 · 5 我的音色 · 6 账本 · 7 API Key · 8 打开 Web · 0 退出**
生成的视频/图片/语音默认保存到系统「下载」目录。

### 窗口 / 浏览器（Web UI）
- Windows：双击 exe，内嵌 WebView2 窗口（1280×880，居中）。
- Linux：运行后自动打开默认浏览器访问 `http://127.0.0.1:8731`；用 `--web` 强制。
- macOS：**非支持平台**，仅供开发调试（同样开浏览器访问）；正式使用请用原生 `ClipForge.app`。

### 端口与多实例
固定监听 `127.0.0.1:8731`。若端口已被本应用在占用，新进程会**挂载（attach）**到已有实例并直接打开其界面，而非重复启动。

### 命令行参数
```
clipforge            自动判断(Linux 终端→CLI；其他→窗口/浏览器)
clipforge --cli     强制命令行界面
clipforge --web     强制浏览器/内嵌窗口界面
clipforge -h        显示用法
```

---

## 6. 配置与数据

API Key 与账本为**明文**存储在本机用户配置目录，**自行保管、切勿分享或提交到仓库**。

| 文件 | 路径（各系统 `UserConfigDir` 下 `ClipForge/`） |
|---|---|
| 设置 `settings.yml`（含 `apiKey`） | Windows `%APPDATA%\ClipForge\settings.yml` · Linux `~/.config/ClipForge/settings.yml` · macOS `~/Library/Application Support/ClipForge/settings.yml`（仅调试用） |
| 账本 `bill.jsonl`（JSONL，追加式） | 同上目录 |

---

## 7. HTTP API 端点

后端是一个本地 HTTP 服务，前端/CLI 均通过它访问 DashScope。

| 路径 | 方法 | 说明 |
|---|---|---|
| `/` | GET | 内嵌静态前端（SPA） |
| `/api/config` | GET / POST | 读/写 API Key（GET 只返回 `configured: true/false`，不回传 Key） |
| `/api/voice/create` | POST | 声音克隆，创建 `voice_id`（代理 DashScope） |
| `/api/voice/list` | POST | 音色列表 / 查询 |
| `/api/video/submit` | POST | 视频任务提交 |
| `/api/video/task` | GET | 视频任务查询（`?taskId=xxx`） |
| `/api/video/download` | GET | 视频下载代理（`?url=xxx`） |
| `/api/upload` | POST | 本地文件 → DashScope OSS（返回 `oss://` resource，供克隆/视频引用） |
| `/api/image` | POST | 文生图（同步接口） |
| `/api/bill` | GET / POST / PUT / DELETE | 账本：列出 / 新增 / 回填状态 / 清空 |
| `/api/bill/export` | GET | 导出 CSV（带 BOM，字段已转义） |
| `/api/tts/ws` | WebSocket | CosyVoice TTS 全双工代理（服务端注入鉴权，前端不接触 Key） |

### 后端实际调用的 DashScope 接口
- 基础：`https://dashscope.aliyuncs.com/api/v1`
- 视频提交：`/services/aigc/video-generation/video-synthesis`
- 视频查询：`/tasks/{taskId}`
- 文生图：`/services/aigc/multimodal-generation/generation`
- 声音克隆：`/services/audio/tts/customization`
- 文件上传：`/api/v1/uploads`（先 `get_policy` 取临时凭证，直传 OSS，再 `submit` 通知资源就绪）
- TTS WebSocket：`wss://dashscope.aliyuncs.com/api-ws/v1/inference`

---

## 8. 价目表（华北2·北京，按量付费，仅供参考，以官方账单为准）

| 类型 | 模型 | 计费 |
|---|---|---|
| 视频 | `wan2.6-i2v` / `wan2.7-i2v` / `wan2.6-t2v` / `wan2.7-t2v` | ¥0.6（720P）/ ¥1.0（1080P）**元/秒**（有声/无声同价） |
| 视频 | `wan2.6-i2v-flash` | 有声 ¥0.3（720P）/¥0.5（1080P）；无声 ¥0.15/¥0.25 **元/秒** |
| 图片 | `qwen-image-2.0` / `wan2.7-image` | ¥0.20 / 张 |
| 图片 | `qwen-image-2.0-pro` / `wan2.7-image-pro` | ¥0.50 / 张 |
| 语音 | `cosyvoice-v3.5-plus` | ¥1.50 / 万字符 |
| 克隆 | `voice-enrollment` | 随训练/首次合成出账（约 ¥0.3–¥2，波动较大） |

账本中的「预估金额」按上表在本地估算，仅用于核对；最终以 DashScope 实际扣费为准。

---

## 9. 工程结构

```
clipforge-web/
├── server/                 # Go 后端(单文件 HTTP server + 内嵌前端)
│   ├── main.go             # 服务/路由/配置/账单/上传/TTS-WS 代理
│   ├── cli.go              # 终端命令行菜单(视频/图片/语音/克隆/账本)
│   ├── webview_win.go      # Windows: 内嵌 WebView2 窗口(关窗=退出)
│   ├── webview_linux_nocgo.go / webview_other.go  # 非 Windows: 打开系统浏览器
│   ├── cli_term_linux.go / cli_term_other.go      # 终端能力判定
│   ├── go.mod / go.sum / vendor/   # 已 vendored, 离线可编译
│   └── static/             # 前端 SPA(被 go:embed 内嵌)
│       ├── index.html
│       ├── style.css
│       └── app.js
├── assets/
│   └── icon-512.png        # deb 桌面图标
├── build-pkgs.py           # 交叉编译 + zip + deb 一键脚本(产物 → release/)
├── .github/workflows/build-web.yml   # tag v* 时自动构建 Linux 产物
├── LICENSE
└── README.md
```

依赖：`github.com/gorilla/websocket`（TTS WS 转发）、`github.com/jchv/go-webview2`（Windows 内嵌窗口，已内嵌 WebView2Loader）。`go 1.23`，全部 `CGO_ENABLED=0` 静态编译。

---

## 10. 常见问题

- **API Key 怎么填？** 打开「设置」页填入阿里云百炼（DashScope）API Key，保存到本机 `settings.yml`；CLI 下首次使用也会引导输入（不回显）。获取地址：https://bailian.console.aliyun.com/ → API-KEY 管理。
- **视频一直转圈？** 视频为异步任务，后端会每 8s 轮询最长 30 分钟；超时仍在后台跑，可稍后到 Web 界面查看。
- **声音克隆失败？** 参考音频需为 3–10s 清晰人声、无背景音；失败通常是音频质量不达标（返回 `UNDEPLOYED`）。
- **本地端口被占？** 见第 5 节「端口与多实例」——新进程会挂载到已有实例。
- **macOS 能当正式版用吗？** 不建议；官方 macOS 走原生 SwiftUI 版。

---

## 11. License

见仓库根 `LICENSE`（Apache-2.0）。
