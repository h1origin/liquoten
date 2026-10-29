#!/usr/bin/env bash
# 打包 liquoten.app：swiftc 编译菜单栏 App，go build 编译 daemon 放进 Resources。
# 环境变量：ARCHS（默认本机架构，发版用 "arm64 x86_64" 生成通用版本）、VERSION（写入 Info.plist）。
set -euo pipefail
cd "$(dirname "$0")/.."

APP_NAME="liquoten"
BUILD_DIR="build"
APP="$BUILD_DIR/$APP_NAME.app"
HOST_ARCH="$(uname -m)"
ARCHS="${ARCHS:-$HOST_ARCH}"
SOURCES=(app/main.swift app/Models.swift app/DaemonManager.swift
  app/NotificationConsumer.swift app/Updater.swift app/Installer.swift app/Gourd.swift app/Dashboard.swift app/AppDelegate.swift)

echo "==> 清理旧产物"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

swift_parts=()
go_parts=()
for arch in $ARCHS; do
  echo "==> 编译 Swift App（${arch}）"
  swiftc -O -target "$arch-apple-macos13.0" -o "$TMP/app-$arch" "${SOURCES[@]}"
  swift_parts+=("$TMP/app-$arch")

  echo "==> 编译 Go daemon（${arch}）"
  goarch="$arch"
  [ "$arch" = "x86_64" ] && goarch="amd64"
  GOOS=darwin GOARCH="$goarch" CGO_ENABLED=0 \
    go build -trimpath -o "$TMP/daemon-$arch" ./cmd/liquoten
  go_parts+=("$TMP/daemon-$arch")
done
lipo -create -output "$APP/Contents/MacOS/$APP_NAME" "${swift_parts[@]}"
lipo -create -output "$APP/Contents/Resources/liquoten" "${go_parts[@]}"
# 文件系统默认不区分大小写，主程序被 Go daemon 覆盖时 App 会无界面运行，这里兜底拦截。
if go version "$APP/Contents/MacOS/$APP_NAME" 2>/dev/null | grep -q ": go1"; then
  echo "错误: $APP/Contents/MacOS/$APP_NAME 是 Go 程序而不是 Swift App" >&2
  exit 1
fi

echo "==> 生成图标"
swiftc -O -target "$HOST_ARCH-apple-macos13.0" -o "$TMP/make-icon" app/Gourd.swift app/icon/main.swift
"$TMP/make-icon" "$TMP/AppIcon.iconset"
iconutil -c icns -o "$APP/Contents/Resources/AppIcon.icns" "$TMP/AppIcon.iconset"

echo "==> 组装 bundle"
cp app/Info.plist "$APP/Contents/Info.plist"
if [ -n "${VERSION:-}" ]; then
  plutil -replace CFBundleShortVersionString -string "$VERSION" "$APP/Contents/Info.plist"
  plutil -replace CFBundleVersion -string "$VERSION" "$APP/Contents/Info.plist"
fi
codesign --force -s - "$APP/Contents/Resources/liquoten"
codesign --force -s - "$APP"

echo "==> 完成: ${APP}（$(lipo -archs "$APP/Contents/MacOS/$APP_NAME")）"
