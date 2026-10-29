# 任务 02：在“服务”页给百炼填写 AK/SK

## 背景

任务 01 之后，百炼有 AK/SK 时直连查询（`internal/providers/bailian/direct.go`），AK/SK 来源有两个：
liquoten 配置里的 `access_key_id` / `access_key_secret`（优先），或 `~/.bailian/config.json`（`bl auth login --open-api` 写入的）。
目前 App 里没有地方填 AK/SK，只能手改配置或用 `bl`。本任务在 App 的“服务”页百炼那一行加上输入框。

开始前必须先读仓库根目录的 `AGENTS.md`，所有硬性规则都适用。

## 范围

- Go：`cmd/liquoten/main.go`、`internal/config/`、`internal/providers/`（只加一个可选接口）、`internal/providers/bailian/`。
- Swift：`app/Dashboard.swift`、`app/AppDelegate.swift`、`app/DaemonManager.swift`、`app/Models.swift`。
- 不加新 provider，不引入第三方依赖，不改其他 provider 的行为。新增 Swift 文件的话要同步加进 `app/build.sh` 的 `SOURCES`（尽量不新增）。

## 要做的事

### 1. 配置写入（`internal/config/`）

- `ProviderUpdate` 增加 `AccessKeyID`、`AccessKeySecret`（`*string`），`SetProvider` 写入 `access_key_id` / `access_key_secret`，写法与 `api_key` 一致。
- 补单测：写入、覆盖、`Delete` 删除这两个键，其余内容和注释保持不变。

### 2. AK 状态（`internal/providers/` + `bailian/`）

- `internal/providers/provider.go` 新增可选接口：
  ```go
  type AccessKeyReporter interface {
      AccessKeyStatus() (source string, maskedID string)
  }
  ```
  `source` 取值：`"config"`（来自 liquoten 配置）、`"bl"`（来自 `~/.bailian/config.json`）、`""`（都没有）。
  `maskedID` 形如 `LTAI…ytkt`（前 4 位 + `…` + 后 4 位；长度不足 8 位时只显示 `…` + 后 4 位）；没有时为空。
  **任何情况下都不返回 secret。**
- 百炼 provider 实现该接口（调用前按现有逻辑重新读 `~/.bailian/config.json`），补单测覆盖三种来源和掩码。

### 3. daemon 子命令（`cmd/liquoten/main.go`，只做装配）

- `providers` 子命令的输出项增加 `access_key_source`、`access_key_masked`（provider 实现了 `AccessKeyReporter` 才填，否则为空字符串）。
- 新增 `liquoten set-access-key [-config 路径] <provider>`：
  - 从**标准输入**读两行：第一行 AK ID，第二行 secret（去掉首尾空白），任何一项为空就报错 `请填写 AccessKey ID 和 AccessKey Secret`。
  - 用这对 AK/SK 构造 provider（`Options.AccessKeyID/AccessKeySecret`），30 秒超时调一次 `Query` 验证；失败直接返回错误，**不写配置**。
  - 验证通过后 `SetProvider` 写入 `access_key_id`、`access_key_secret` 并设 `enabled = true`。
  - provider 不支持 AK（没实现 `AccessKeyReporter`）时报错。
- 新增 `liquoten clear-access-key [-config 路径] <provider>`：从配置里删掉 `access_key_id`、`access_key_secret`，不改 `enabled`。
- 现有 `disable` 子命令删除键时，除了 `api_key` 也删掉 `access_key_id`、`access_key_secret`（与“关闭服务时删除密钥”的约定一致）。
- 错误信息里不能出现 secret（provider 层已经脱敏，这里不要把 stdin 内容拼进错误）。

### 4. App（Swift）

- `ProviderInfo` 解码 `access_key_source`、`access_key_masked`（缺失时为空字符串，兼容旧 daemon）。
- `DaemonManager` 增加 `setAccessKey(provider:id:secret:completion:)`（经 stdin 传 `id + "\n" + secret`，不出现在进程参数里）和 `clearAccessKey(provider:completion:)`。
- 服务页百炼那一行（`ServiceRow`），**无论开关是否打开都显示** AK 区域：
  - 已有 AK（source 非空）且不在编辑状态：显示一行 `AccessKey：LTAI…ytkt（liquoten 配置）` 或 `（来自 bl 配置）`，旁边按钮“修改”；source 为 `config` 时再加“清除”。
  - 没有 AK，或点了“修改”：显示 `TextField("AccessKey ID")`、`SecureField("AccessKey Secret")` 和按钮“保存并验证”（两项都非空才可点；编辑状态下再加“取消”）。
  - 点“保存并验证”：进入忙碌状态（复用 `togglingProvider` / `busyText`，文字“正在验证 AccessKey…”），调用 `setAccessKey`；成功后清空输入框、退出编辑、`loadProviders()` + `forceRefresh()`；失败用现有 `finish` 弹窗，标题“AccessKey 验证失败”，输入框内容保留。
  - 点“清除”：调用 `clearAccessKey`，完成后 `loadProviders()` + `forceRefresh()`。
  - 输入框下方一行说明（caption、secondary）：`建议使用只授予百炼权限的 RAM 子账号。AccessKey 以明文保存在 ~/.config/liquoten/config.toml，关闭服务时删除。`
- “登录国内站 / 登录国际站”按钮只在服务已开启且**没有 AK**（source 为空）时显示。
- 百炼的 `providerDescription` 改为：`查询 Token Plan 用量。填写阿里云 AccessKey 即可直接查询；也可以安装官方命令行工具 bl 并登录百炼控制台。`
- `friendlyError` 增加百炼直连常见错误的友好提示（按包含关系匹配，放在现有规则之前）：
  - `NotAuthorised` → `这个 AccessKey 没有查询 Token Plan 用量的权限，请在 RAM 控制台给它授予百炼权限，或换用订阅套餐的账号。`
  - `InvalidAccessKeyId`、`SignatureDoesNotMatch`、`MissingAccessKeyId` → `AccessKey ID 或 Secret 不正确。`
- 打开百炼开关时如果没装 `bl` 且没有 AK，现有流程会弹“需要先安装 bl”。把这个弹窗的说明文字补一句：`也可以取消后，在服务页填写阿里云 AccessKey，无需安装 bl。`

## 验收

```bash
go test ./... && go vet ./... && test -z "$(gofmt -l .)" && ./app/build.sh
```

全部通过（`./app/build.sh` 要能编译出 `build/Liquoten.app`），并按 AGENTS.md 提交一次 commit。
`build/` 下的产物不要提交（确认在 `.gitignore` 里）。
