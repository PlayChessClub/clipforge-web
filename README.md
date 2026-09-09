# ClipForge Web (Go + HTML5)

> v2.0.0+ 替代旧版 WinUI 3 实现。**零运行时依赖、单文件 ~6MB，主要面向 Windows；Linux 亦有 CLI/浏览器产物**。
> 说明：macOS 请使用原生 SwiftUI 版（仓库根 `build.sh` 产出 ClipForge.app）；本 Web 版**不再把 macOS 列为支持平台**（darwin 上 `--cli / --web` 仅作本地开发调试，不属正式支持）。

## 怎么跑

### 1) 装 Go

下载安装：https://go.dev/dl/ （需要 Go 1.21+）

### 2) 启动

```bash
cd web/server
go run .
# Windows: 自动弹出内嵌 WebView2(Chromium)应用窗口,关窗即退出
# Linux: 自动打开系统浏览器 http://127.0.0.1:8731（也支持 CLI/桌面图标入口）
# macOS: 非支持平台——请用原生 ClipForge.app；darwin 强制本服务仅供本地调试
```

> Windows 版用 **WebView2**(Edge 的 Chromium 内核)渲染,Win10/Win11 自带运行时,
> 无需额外安装。WebView2Loader.dll 已内嵌进 exe,仍是**单文件**。
> 若系统缺失 WebView2(极老的精简系统),自动退回打开默认浏览器。

### 3) 打包成单文件 exe(Windows)

```powershell
# Windows PowerShell
cd web\server
$env:GOOS="windows"; $env:GOARCH="amd64"
go build -ldflags="-s -w" -o ..\..\clipforge.exe .
# ARM64 用 arm64 替换 amd64
```

最终 `clipforge.exe` 双击就开,弹出内嵌 Chromium 应用窗口,无需手动开浏览器。

### 4) 配置 API Key

首次启动后,在应用「设置」页填入 DashScope API Key,保存到本地明文 yml。

## 文件布局

```
web/
├── server/
│   ├── main.go             # Go HTTP server,代理 DashScope + 静态服务
│   ├── webview_win.go      # Windows: 内嵌 WebView2 窗口(关窗=退出)
│   ├── webview_other.go    # macOS/Linux: 打开系统浏览器
│   ├── go.mod
│   └── go.sum
└── static/
    ├── index.html     # 单页 SPA
    ├── style.css      # 暗色玻璃风
    └── app.js         # 原生 JS,无框架
```

## API 端点

| 路径 | 方法 | 说明 |
|---|---|---|
| `/` | GET | 静态文件 |
| `/api/config` | GET/POST | 读写 API Key(GET 只返回 `configured: true/false`) |
| `/api/voice/create` | POST | 声音克隆(创建 voice_id) |
| `/api/voice/list` | GET | 音色列表 |
| `/api/video/submit` | POST | 视频任务提交 |
| `/api/video/task` | GET | 视频任务查询(`?taskId=xxx`) |
| `/api/video/download` | GET | 视频下载代理 |
| `/api/upload` | POST | 本地文件 → DashScope OSS(返回 resource URL) |

## 配置路径

- Windows: `%APPDATA%\ClipForge\settings.yml`
- macOS: `~/Library/Application Support/ClipForge/settings.yml`
- Linux: `~/.config/ClipForge/settings.yml`

明文存储,**自行保管,不要提交到代码仓库**。

## 已实现

- ✅ 声音克隆(cosyvoice-v3.5-plus)+ 音色列表
- ✅ 图生视频(wan2.6-i2v)+ 提示词/分辨率/时长/镜头/AI 增强
- ✅ 本地文件上传到 DashScope OSS

## 不实现

- ❌ 实时 TTS WebSocket 合成(协议复杂,踩过 task-failed 等坑)
- ❌ 视频时间线拼接(纯前端拼接需 ffmpeg.wasm,体积巨大且性能差)
