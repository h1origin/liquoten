#!/usr/bin/env bash
# 生成发布用的 dmg：通用版本（Apple 芯片 + Intel）的 Liquoten.app 加“应用程序”快捷方式。
# 用法：./app/package-dmg.sh 1.2.0  → build/Liquoten-1.2.0.dmg
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:?用法: $0 <版本号，如 1.2.0>}"
APP_NAME="Liquoten"
DMG="build/$APP_NAME-$VERSION.dmg"

ARCHS="arm64 x86_64" VERSION="$VERSION" ./app/build.sh

echo "==> 生成 dmg"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
cp -R "build/$APP_NAME.app" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
rm -f "$DMG"
hdiutil create -volname "$APP_NAME $VERSION" -srcfolder "$STAGE" -fs HFS+ -format UDZO -ov "$DMG" >/dev/null

echo "==> 完成: ${DMG}（$(du -h "$DMG" | cut -f1)）"
