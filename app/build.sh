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
  app/NotificationConsumer.swift app/Dashboard.swift app/AppDelegate.swift

echo "==> 编译 Go daemon"
GOOS=darwin GOARCH="$ARCH" CGO_ENABLED=0 \
  go build -o "$APP/Contents/Resources/liquoten" ./cmd/liquoten

echo "==> 组装 bundle"
cp app/Info.plist "$APP/Contents/Info.plist"
codesign --force -s - "$APP" 2>/dev/null || true

echo "==> 完成: $APP"
