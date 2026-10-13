#!/usr/bin/env bash
# 打包 Liquoten.app：swiftc 编译菜单栏 App，go build 编译 daemon 放进 Resources。
set -euo pipefail
cd "$(dirname "$0")/.."

APP_NAME="Liquoten"
BUILD_DIR="build"
APP="$BUILD_DIR/$APP_NAME.app"
ARCH="$(uname -m)"

echo "==> 清理旧产物"
rm -rf "$APP"

echo "==> 编译 Swift App（${ARCH}）"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
swiftc -O -target "$ARCH-apple-macos13.0" \
  -o "$APP/Contents/MacOS/$APP_NAME" \
  app/main.swift app/Models.swift app/DaemonManager.swift \
  app/NotificationConsumer.swift app/Droplet.swift app/Dashboard.swift app/AppDelegate.swift

echo "==> 编译 Go daemon"
GOOS=darwin GOARCH="$ARCH" CGO_ENABLED=0 \
  go build -o "$APP/Contents/Resources/liquoten" ./cmd/liquoten

echo "==> 生成图标"
ICON_TMP="$(mktemp -d)"
swiftc -O -target "$ARCH-apple-macos13.0" -o "$ICON_TMP/make-icon" app/Droplet.swift app/icon/main.swift
"$ICON_TMP/make-icon" "$ICON_TMP/AppIcon.iconset"
iconutil -c icns -o "$APP/Contents/Resources/AppIcon.icns" "$ICON_TMP/AppIcon.iconset"
rm -rf "$ICON_TMP"

echo "==> 组装 bundle"
cp app/Info.plist "$APP/Contents/Info.plist"
codesign --force -s - "$APP" 2>/dev/null || true

echo "==> 完成: $APP"
