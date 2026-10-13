#!/bin/sh
# 测试用 fake bl：支持 usage token-plan 与 auth login --console，输出录制好的 fixture。
if [ "$1" = auth ]; then
  [ "$2 $3 $4" = "login --console --console-site" ] || { echo "unexpected auth args: $*" >&2; exit 1; }
  case "$5" in
    domestic|international) exit 0 ;;
    *) echo "bad site: $5" >&2; exit 1 ;;
  esac
fi
case "$2" in
  token-plan) cat "$(dirname "$0")/tokenplan.json" ;;
  *) echo "unexpected command: $2" >&2; exit 1 ;;
esac
