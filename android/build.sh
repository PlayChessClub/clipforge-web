#!/bin/bash
# ClipForge Android 打包脚本(免 Gradle:aapt2 + javac + d8 + zipalign + apksigner)
#
# 架构:Go 后端(clipforge-web/server)交叉编译为 android 可执行文件,
#       以 libclipforge.so 打进 APK 的 lib/<abi>/;Activity 启动时 exec 它,
#       WebView 加载其内嵌的 Web UI(http://127.0.0.1:8731)。
set -e

ROOT="$(cd "$(dirname "$0")" && pwd)"
SDK="${ANDROID_SDK:-$HOME/android-sdk}"
BT="$(ls -d "$SDK"/build-tools/* 2>/dev/null | sort -V | tail -1)"
PLATFORM="$(ls -d "$SDK"/platforms/android-* 2>/dev/null | sort -V | tail -1)/android.jar"
GO="${GO_BIN:-$HOME/go-sdk/go/bin/go}"
# 后端源码:默认取同级 server/ 目录(本仓库内),可用 CLIPFORGE_WEB 覆盖
SERVER="${CLIPFORGE_WEB:-$(cd "$ROOT/../server" && pwd)}"

# 版本号统一取自 build-pkgs.py(单一事实来源),可用 CF_VERSION 覆盖
VERSION="${CF_VERSION:-$(sed -n 's/^VERSION = .*"\([0-9][0-9.]*\)".*/\1/p' "$ROOT/../build-pkgs.py" | head -1)}"
[ -z "$VERSION" ] && VERSION="3.3.1"
VERSION_CODE=$(echo "$VERSION" | awk -F. '{printf "%d", $1*10000 + $2*100 + $3}')

BUILD="$ROOT/build"
OUT="$ROOT/out"
ABIS="arm64-v8a"

echo "▶ 清理"
rm -rf "$BUILD" "$OUT"
mkdir -p "$BUILD/dex" "$BUILD/classes" "$OUT"

echo "▶ 交叉编译 Go 后端(android)"
mkdir -p "$BUILD/lib"
for abi in $ABIS; do
  case "$abi" in
    arm64-v8a)   goarch=arm64 ;;
    armeabi-v7a) goarch=arm   ;;
    x86_64)      goarch=amd64 ;;
  esac
  mkdir -p "$BUILD/lib/$abi"
  ( cd "$SERVER" && GOOS=android GOARCH=$goarch CGO_ENABLED=0 \
      "$GO" build -ldflags="-s -w" -o "$BUILD/lib/$abi/libclipforge.so" . )
  echo "   ✓ $abi ($(du -h "$BUILD/lib/$abi/libclipforge.so" | cut -f1))"
done

echo "▶ aapt2 编译资源"
"$BT/aapt2" compile --dir "$ROOT/res" -o "$BUILD/res.zip"

echo "▶ aapt2 链接(生成带 manifest/资源的基础 APK)"
# 注意:aapt2 的 --version-code/--version-name 不会覆盖 manifest 里已写死的值,
# 所以先把版本号注入一份临时 manifest 再 link
MANIFEST="$BUILD/AndroidManifest.xml"
sed -e "s/android:versionCode=\"[0-9]*\"/android:versionCode=\"$VERSION_CODE\"/" \
    -e "s/android:versionName=\"[^\"]*\"/android:versionName=\"$VERSION\"/" \
    "$ROOT/AndroidManifest.xml" > "$MANIFEST"
echo "   版本 $VERSION (versionCode $VERSION_CODE)"
"$BT/aapt2" link \
  -o "$BUILD/base.apk" \
  -I "$PLATFORM" \
  --manifest "$MANIFEST" \
  --min-sdk-version 24 --target-sdk-version 34 \
  "$BUILD/res.zip"

echo "▶ javac 编译 Java"
javac -encoding UTF-8 --release 11 -nowarn \
  -cp "$PLATFORM" \
  -d "$BUILD/classes" \
  "$ROOT/src/com/clipforge/app/MainActivity.java"

echo "▶ d8 生成 dex"
"$BT/d8" --release --lib "$PLATFORM" --min-api 24 \
  --output "$BUILD/dex" \
  "$BUILD/classes/com/clipforge/app/"*.class

echo "▶ 组装 APK(dex + 各 ABI 原生库)"
cp "$BUILD/base.apk" "$BUILD/unsigned.apk"
( cd "$BUILD/dex" && zip -q "$BUILD/unsigned.apk" classes.dex )
( cd "$BUILD" && zip -qr unsigned.apk lib )

echo "▶ 生成签名密钥(首次)"
KS="$ROOT/keystore/clipforge.jks"
if [ ! -f "$KS" ]; then
  mkdir -p "$ROOT/keystore"
  keytool -genkeypair -keystore "$KS" -alias clipforge \
    -keyalg RSA -keysize 2048 -validity 10000 \
    -storepass clipforge -keypass clipforge \
    -dname "CN=ClipForge, OU=Dev, O=ClipForge, L=Shenzhen, ST=Guangdong, C=CN" >/dev/null 2>&1
  echo "   ✓ 已生成 $KS"
fi

echo "▶ zipalign + 签名"
"$BT/zipalign" -f 4 "$BUILD/unsigned.apk" "$BUILD/aligned.apk"
"$BT/apksigner" sign \
  --ks "$KS" --ks-pass pass:clipforge --key-pass pass:clipforge \
  --out "$OUT/ClipForge-android.apk" "$BUILD/aligned.apk"

echo "▶ 校验"
"$BT/apksigner" verify --print-certs "$OUT/ClipForge-android.apk" | head -5
ls -lh "$OUT/ClipForge-android.apk"
echo "✅ 完成 → $OUT/ClipForge-android.apk"
