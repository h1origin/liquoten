#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "usage: $0 <current-tag> [previous-tag]" >&2
  exit 1
fi

current="$1"
previous="${2:-}"
if [[ -z "$previous" ]]; then
  previous="$(git describe --tags --abbrev=0 "${current}^" 2>/dev/null || true)"
fi

if [[ -n "$previous" ]]; then
  subjects="$(git log --no-merges --format=%s "${previous}..${current}")"
else
  subjects="$(git log --no-merges --format=%s "${current}")"
fi

features=()
fixes=()
improvements=()

while IFS= read -r subject; do
  [[ -z "$subject" ]] && continue
  [[ "$subject" == *:* ]] || continue
  prefix="${subject%%:*}"
  rest="${subject#*:}"
  summary="${rest#"${rest%%[![:space:]]*}"}"
  [[ -z "$summary" ]] && continue

  breaking=0
  if [[ "$prefix" == *"!" ]]; then
    breaking=1
    prefix="${prefix%!}"
  fi

  topic=""
  if [[ "$prefix" == *"("*")" ]]; then
    kind="${prefix%%(*}"
    topic="${prefix#*(}"
    topic="${topic%%)*}"
    [[ "$prefix" == "${kind}(${topic})" ]] || continue
  else
    kind="$prefix"
  fi
  case "$kind" in
    ""|*[!a-z]*) continue ;;
  esac

  if [[ -n "$topic" ]]; then
    line="- **${topic}**: ${summary}"
  else
    line="- ${summary}"
  fi
  if [[ "$breaking" == 1 ]]; then
    line="${line} ⚠️"
  fi
  case "$kind" in
    feat) features+=("$line") ;;
    fix) fixes+=("$line") ;;
    refactor|perf) improvements+=("$line") ;;
  esac
done <<< "$subjects"

if [[ -n "$previous" ]]; then
  echo "**Full changelog**: https://github.com/h1origin/liquoten/compare/${previous}...${current}"
  echo
fi

if ((${#features[@]})); then
  echo "### ✨ New features / 新功能"
  printf '%s\n' "${features[@]}"
  echo
fi
if ((${#fixes[@]})); then
  echo "### 🐛 Bug fixes / 问题修复"
  printf '%s\n' "${fixes[@]}"
  echo
fi
if ((${#improvements[@]})); then
  echo "### 🔧 Improvements / 改进"
  printf '%s\n' "${improvements[@]}"
  echo
fi
if ((${#features[@]} == 0 && ${#fixes[@]} == 0 && ${#improvements[@]} == 0)); then
  echo "- Maintenance release / 维护更新"
  echo
fi

version="${current#v}"
cat <<EOF
## ⬆️ Upgrade / 升级

- **v1.0.12 or later**: click **Update now** in the app — no Gatekeeper prompt.
  **v1.0.12 及以上**：在 App 里点 **立即更新**，不会被系统拦截。
- **Older versions**: download the dmg below and replace the app.
  **更早版本**：下载下方 dmg 覆盖安装。

## 📦 Install / 安装

- **Download** \`liquoten-${version}.dmg\`, drag **liquoten** into **Applications**.
  **下载** \`liquoten-${version}.dmg\`，把 **liquoten** 拖进「应用程序」。
- **First launch is blocked** (not notarized yet): System Settings → Privacy & Security → **Open Anyway**, or run \`xattr -dr com.apple.quarantine /Applications/liquoten.app\`.
  **首次打开会被拦截**（暂未公证）：系统设置 → 隐私与安全性 → **仍要打开**，或执行上面的命令。
- **Get started**: click the gourd icon in the menu bar → **Services**.
  **开始使用**：点菜单栏葫芦图标 → **服务**。

## 💻 Requirements / 系统要求

- **macOS 13+**, Apple silicon & Intel.  **macOS 13 及以上**，支持 Apple 芯片和 Intel。
- **Checksum**: \`liquoten-${version}.dmg.sha256\` is attached.  **校验**：附带 \`.sha256\` 文件。
EOF