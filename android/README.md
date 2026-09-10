# ClipForge Android

ClipForge 的安卓版：**WebView 壳 + 内嵌 Go 后端**。

它不重写业务逻辑，而是把 web 版（`clipforge-web`）的 Go 后端交叉编译成 Android 可执行文件，
随 APK 一起分发；App 启动时在本地拉起该后端（`127.0.0.1:8731`），再用 WebView 加载它内嵌的 Web UI。
因此**功能与 web 版完全一致**：图片生成、视频生成、语音合成、声音克隆、我的音色、账本。

## 架构

```
┌─────────────────────── Android App ───────────────────────┐
│  MainActivity (Java)                                       │
│    ├─ 启动时 exec: lib/arm64-v8a/libclipforge.so           │  ← Go 后端(交叉编译)
│    ├─ 轮询 http://127.0.0.1:8731/api/config 直到就绪        │
│    └─ WebView.loadUrl("http://127.0.0.1:8731")             │
│           │                                                │
│           ├─ JS 桥 AndroidBridge: 接管 blob:/http 下载      │
│           ├─ onShowFileChooser: 声音克隆的参考音频上传       │
│           └─ DownloadListener → 系统下载器(图片/视频)        │
└────────────────────────────────────────────────────────────┘
              │ HTTPS
              ▼
      阿里云百炼 DashScope（API Key 存在 App 私有目录，不上传）
```

Go 后端以 `libclipforge.so` 之名放在 `lib/<abi>/` 下，安装时被系统解压到
应用私有原生库目录（可执行），App 通过 `Runtime.exec` 拉起——这是 Android 上分发
可执行文件的标准做法（无需 NDK / gomobile）。

## 目录

```
clipforge-android/
├── AndroidManifest.xml                     # 权限、cleartext(127.0.0.1)、extractNativeLibs=true
├── src/com/clipforge/app/MainActivity.java # 壳：启后端 + WebView + 下载/上传接管
├── res/                                    # 图标、字符串、网络安全配置
├── build.sh                                # 免 Gradle 打包脚本
├── keystore/                               # 本地签名密钥(勿提交)
└── out/ClipForge-android.apk               # 产物
```

## 构建

**依赖**：Go（交叉编译后端）、JDK、Android SDK（`platforms;android-34` + `build-tools`）。

```bash
# 1) 准备 Android SDK（若尚未安装）
#    下载 commandline-tools 后：
sdkmanager --sdk_root=$HOME/android-sdk "platforms;android-34" "build-tools;37.0.0"

# 2) 打包
export ANDROID_SDK=$HOME/android-sdk
export GO_BIN=$HOME/go-sdk/go/bin/go
export CLIPFORGE_WEB=$HOME/WorkBuddy/2026-09-07-06-52-26/clipforge-web/server
./build.sh
# → out/ClipForge-android.apk
```

脚本流程：交叉编译 Go（`GOOS=android GOARCH=arm64 CGO_ENABLED=0`）→ `aapt2 compile/link`
→ `javac` → `d8` → 组装（dex + `lib/`）→ `zipalign` → `apksigner` 签名。

> 免 Gradle 的原因：本机 `maven.google.com` 不可达，Gradle 依赖解析会失败；
> 该链路只依赖 `dl.google.com` 的 SDK 组件，更稳。

## 安装

```bash
adb install -r out/ClipForge-android.apk
# 或把 APK 传到手机，点击安装（需在系统设置里允许「未知来源」）
```

首次打开后，在「设置」页填入阿里云百炼 API Key 即可使用。

## 已知限制 / 待办

- **仅 arm64-v8a**：32 位 ARM 与 x86_64 的 Go 交叉编译需要 NDK（外部链接），当前未接入。
  现代安卓真机与 Apple Silicon 模拟器均为 arm64，够用。
- **未在真机验证**：本机无 Android 设备/模拟器，APK 仅完成静态校验（结构、manifest、签名）。
- **Web UI 仍是桌面布局**：手机竖屏下偏挤，移动端自适应属于下一步「补齐功能」的范围。
- **自签名证书**：用于开发分发；如需上架应用商店需另行处理。
- 签名密钥 `keystore/clipforge.jks`（口令 `clipforge`）仅本地开发用，**不要提交到仓库**。
