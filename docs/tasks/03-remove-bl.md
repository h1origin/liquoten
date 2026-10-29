# 任务 03：移除 bl（bailian-cli）依赖，百炼只走 AK/SK 直连

## 背景

任务 01、02 之后，百炼可以用阿里云 AK/SK 直连查询，并且已经用真实账号验证通过。
现在把和 `bl` 有关的一切全部移除：执行 `bl` 查询、浏览器登录、安装检测、npm 安装、读 `~/.bailian/config.json`，
以及 App 里对应的按钮、弹窗和文案。之后百炼只认 liquoten 配置里的 `access_key_id` / `access_key_secret`。

开始前必须先读仓库根目录的 `AGENTS.md`，所有硬性规则都适用。

## 要移除的内容

### Go

- `internal/providers/bailian/bailian.go`：执行 `bl` 的整条路径——`run`、`lookup`、`searchDirs`、`withDirInPath`、
  `Login`、`Preflight`、`Install`、`loginTimeout`，以及 `command` / `apiKey` 字段。`Query` 没有 AK/SK 时直接返回错误
  `未配置阿里云 AccessKey，请在服务页填写`（不要再回退到 bl）。
- `internal/providers/bailian/direct.go`：读 `~/.bailian/config.json` 的部分（`bailianConfigPath`、`bailianConfig`、`readBLConfig`、
  每次 Query 重读配置的 `refreshAuth` 逻辑等）。AK/SK 只来自 `Options`，在 `New` 时确定，不再变化，所以
  “凭据变化时清 token”的快照逻辑也可以简化掉（token 缓存、`NotLogined` 重换 token 重试一次保留）。
- region / site / switchAgent 不再从 bl 配置读：固定用 `cn-beijing` / `domestic`、不带 switchAgent。
  `gatewayFor`、`tokenHost` 的四种组合及其测试**保留**（以后可能做成配置项），本任务不加新配置。
- `AccessKeyStatus()`：source 只有 `"config"` 或 `""` 两种。
- `internal/providers/provider.go`：`Authenticator`、`Preflighter`、`Installer`、`ErrNotInstalled` 只有百炼在用，全部删除。
  `Options.Command` 只有百炼在用，一并删除；`Options.APIKey` 保留（Qoder 在用）。
- `internal/config/`：删除 `ProviderCfg.Command`、`ProviderUpdate.Command` 及其写入逻辑。旧配置文件里残留的
  `command = ...` 行在加载时会被 TOML 忽略，确认不报错并补一条单测。
- `cmd/liquoten/main.go`：删除 `login` 子命令、`install` 子命令、`enable` 里的 Preflight/命令路径写回逻辑、
  `exitNotInstalled` 退出码、`providers` 输出里的 `installable` 字段，以及 `Options` 里的 `Command` 透传。
- 测试：删除 `testdata/fake-bl.sh` 和所有依赖它或依赖 bl 配置文件的测试；`config_test.go`、`main_test.go`、
  `alert_test.go` 里用到 `command` / `bl` 字样的地方相应改掉。新增测试：没有 AK/SK 时 `Query` 返回上面的错误。

### App（Swift）

- `DaemonManager`：删除 `login`、`install`，`EnableResult` 去掉 `.notInstalled`（如果只剩 ok/failed，可以改回 `String?` 回调，和 `disable` 一致）。
- `AppDelegate`：删除 `loginBailian`、`confirmInstall`、安装流程分支，以及 `onLogin` 的绑定。
- `Dashboard` / `Models`：删除 `loggingIn`、`onLogin`、`installable`，所有“登录国内站 / 登录国际站”按钮（服务页和主面板错误卡片里都有）。
- 百炼 `providerDescription` 改为：`用阿里云 AccessKey 查询 Token Plan 用量。建议使用只授予百炼权限的 RAM 子账号。`
- AK 区域的来源文案只剩 `liquoten 配置` 一种（去掉“来自 bl 配置”）。
- `friendlyError`：删掉和 bl 有关的规则（`No console access token`、`command not found` / `executable file not found` 那条）；
  新增 `未配置阿里云 AccessKey` → `还没有填写阿里云 AccessKey，请在服务页填写。`
- 打开百炼开关但还没有 AK 时：不要弹窗报错，允许开启，状态行会显示上面的友好提示，AK 输入框就在同一行。

### 文档

- `README.md`：删除所有 `bl` / `bailian-cli` / npm / Node 相关内容（安装步骤、前置依赖里的 Node、工作原理图里的 `bailian-cli (官方 CLI)`、登录说明等），
  改为说明：在服务页填写阿里云 AccessKey（建议 RAM 子账号，只授予百炼权限；明文保存在配置文件，关闭服务时删除）。
  从源码构建的前置依赖去掉 Node。
- `config.example.toml`：百炼段落只保留 `access_key_id` / `access_key_secret` 的说明，删除 `command`、`api_key`（对百炼无效）及 bl 相关注释。
- `.github/workflows/release.yml`：Release 说明里“监控阿里云百炼需要另外安装官方命令行工具”那句改成“监控阿里云百炼需要在服务页填写阿里云 AccessKey”。
- `bailian.go` 包注释改为只描述 AK/SK 直连。
- `docs/tasks/` 下的旧任务说明不用改。

## 验收

```bash
go test ./... && go vet ./... && test -z "$(gofmt -l .)" && ./app/build.sh
```

全部通过，并且仓库里（`docs/tasks/` 和 `.git` 除外）用 `grep -rnE '\bbl\b|bailian-cli|\.bailian|npm' --exclude-dir=.git --exclude-dir=build --exclude-dir=docs .`
不应再有和 bl 相关的结果。按 AGENTS.md 提交一次 commit（本任务说明一起提交，`build/` 不提交）。
