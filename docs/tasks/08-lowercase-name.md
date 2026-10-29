# 任务 08：产品名从 Liquoten 改为全小写 liquoten

## 背景

用户要求产品名首字母小写：所有面向用户的 `Liquoten` 改为 `liquoten`。

开始前必须先读仓库根目录的 `AGENTS.md`，所有硬性规则都适用。

## 要改的

- `app/Info.plist`：`CFBundleName`、`CFBundleDisplayName`（若有）、`CFBundleExecutable` 等显示名和可执行文件名改为 `liquoten`。
  **`CFBundleIdentifier` 保持 `com.liquoten.app` 不变**（改了会被系统当成另一个 App，通知权限、登录项都要重来）。
- `app/build.sh`、`app/package-dmg.sh`：产物改为 `build/liquoten.app`、`build/liquoten-<版本>.dmg`；dmg 卷名、App 内主可执行文件名同步。
  注意 Swift 主程序和 bundle 内 Go daemon（`Contents/Resources/liquoten`）是两个不同文件，主程序在 `Contents/MacOS/` 下，不要冲突。
- `.github/workflows/release.yml`：dmg 路径、Release 标题（`liquoten <版本>`）、安装说明里的名字和路径。
- Swift 源码里所有面向用户的文字（菜单、提示、通知、`accessibilityDescription` 等）。
- Go 里面向用户的文字（如 `cmd/liquoten/main.go`、`internal/alert/alert.go`、`internal/config/config.go` 的提示或注释里的产品名）。
- `README.md`、`README.zh-CN.md`、`config.example.toml`：产品名、`/Applications/liquoten.app/...` 路径、dmg 文件名。
  README 标题保持 `# liquoten`，图片 alt 也改。
- `docs/liquoten-architecture.excalidraw` 里的文字（只改文本内容，PNG 不用重新导出）。

## 不改的

- `CFBundleIdentifier`、`docs/tasks/` 下的历史任务说明、git 历史。
- 已有的告警状态文件、缓存目录、配置路径（都已经是小写 `liquoten`）。

## 兼容性

- macOS 默认文件系统不区分大小写：用户 Claude Code 里配置的 `/Applications/Liquoten.app/Contents/Resources/liquoten statusline` 换成新版后仍然有效，不需要迁移。
- `Updater.swift` 只按标签判断版本，不依赖 dmg 文件名，确认一下即可。

## 验收

```bash
go test ./... && go vet ./... && test -z "$(gofmt -l .)" && ./app/build.sh && ./app/package-dmg.sh 0.0.0-test
```

全部通过，产物为 `build/liquoten.app` 和 `build/liquoten-0.0.0-test.dmg`；然后

```bash
git grep -n 'Liquoten' -- . ':!docs/tasks'
```

不应再有结果（`com.liquoten.app` 本身是小写不受影响）。按 AGENTS.md 提交一次 commit（本任务说明一起提交，`build/` 不提交）。
