#!/bin/sh
# 测试用 fake bl：只支持 usage token-plan，输出录制好的 fixture。
case "$2" in
  token-plan) cat "$(dirname "$0")/tokenplan.json" ;;
  *) echo "unexpected command: $2" >&2; exit 1 ;;
esac
