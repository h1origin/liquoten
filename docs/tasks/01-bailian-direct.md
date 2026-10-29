# 任务 01：百炼直连模式（AK/SK，不依赖 bl）

## 背景

百炼 provider 现在靠执行 `bl usage token-plan --output json` 查额度。`bl auth login --console` 浏览器登录得到的
access token 很快过期，过期后只能重新登录。

`bl` 内部其实只做两件事，Go 可以直接做：

1. 用阿里云 AK/SK 签名调 OpenAPI `GenerateCLIAccessToken`，换一个 CLI access token；
2. 带 token POST 控制台网关，查 Token Plan 用量。

有 AK/SK 时 liquoten 直接走这两步，token 过期就自己再换，用户不用重新登录，也不用装 Node 和 `bl`。
没有 AK/SK 时保持现在的 `bl` 路径，**行为不变**。

开始前必须先读仓库根目录的 `AGENTS.md`，所有硬性规则都适用（分层、只用标准库、fixture 单测、gofmt/vet、每次改动后 commit）。

## 范围

只改 Go：`internal/providers/`、`internal/providers/bailian/`、`internal/config/`、`cmd/liquoten/main.go`（只做透传）、
`config.example.toml`、README 中百炼相关的段落。**不改 `app/` 下的 Swift 代码**，不加新 provider，不引入第三方依赖。

## 接口细节（从 bailian-cli 2.2.1 源码整理，未经真机验证）

### 1. 换 token：`GenerateCLIAccessToken`

- 方法 / URL：`POST https://<host>/modelstudio/cli/generateAccessToken`，无 query，无 body（body 为空字符串）
- host：`console_region` 为 `ap-southeast-1` 时用 `modelstudio.ap-southeast-1.aliyuncs.com`，其余一律 `modelstudio.cn-beijing.aliyuncs.com`
- 签名 ACS3-HMAC-SHA256，请求头：
  - `host`、`x-acs-action: GenerateCLIAccessToken`、`x-acs-version: 2026-02-10`
  - `x-acs-date`：UTC，格式 `2006-01-02T15:04:05Z`（无毫秒）
  - `x-acs-signature-nonce`：随机串（每次不同）
  - `x-acs-content-sha256`：body 的 sha256 hex（空 body 为 `e3b0c442…b855`）
  - `content-type: application/json`
  - 有 STS token 时加 `x-acs-security-token`（本任务不需要，留空即可）
- 签名算法：
  - 参与签名的头 = `host`、`content-type` 以及所有 `x-acs-*`，按名字字典序排序
  - canonicalHeaders = 每个 `name:value\n` 拼接；signedHeaders = 名字用 `;` 连接
  - canonicalRequest = `METHOD\nPATH\nQUERY\ncanonicalHeaders\nsignedHeaders\nbodySha256`
    （注意 canonicalHeaders 自带结尾 `\n`，再用 `\n` 连接下一项，所以中间有一个空行）
  - stringToSign = `ACS3-HMAC-SHA256\n` + hex(sha256(canonicalRequest))
  - signature = hex(HMAC-SHA256(accessKeySecret, stringToSign))
  - `authorization: ACS3-HMAC-SHA256 Credential=<AK ID>,SignedHeaders=<signedHeaders>,Signature=<signature>`
- **签名必须有固定向量单测**（时间和 nonce 可注入）。用以下输入：
  AK ID `LTAItestkeyid`，secret `testsecret`，host `modelstudio.cn-beijing.aliyuncs.com`，
  path `/modelstudio/cli/generateAccessToken`，POST，空 query、空 body，
  date `2026-10-29T08:00:00Z`，nonce `nonce-0001`，期望的 authorization 为：
  `ACS3-HMAC-SHA256 Credential=LTAItestkeyid,SignedHeaders=content-type;host;x-acs-action;x-acs-content-sha256;x-acs-date;x-acs-signature-nonce;x-acs-version,Signature=d4b14ecdbce49d3f269be182400000286dd51d016db599cf5f812b806b94e621`
- 响应：JSON。HTTP 非 2xx 或 `Success == false` 视为失败，错误信息带 `Code` 和 `Message`。
  token 在顶层字段 `cliAccessToken`；为空也视为失败。

### 2. 查用量：控制台网关

- 网关按 region + site 选：

  | console_region | console_site | 网关 host | action |
  |---|---|---|---|
  | cn-beijing（默认） | domestic（默认） | `bailian-cs.console.aliyun.com` | `BroadScopeAspnGateway` |
  | cn-beijing | international | `bailian-cs.console.alibabacloud.com` | `BroadScopeAspnGateway` |
  | ap-southeast-1 | domestic | `modelstudio-cs.console.aliyun.com` | `IntlBroadScopeAspnGateway` |
  | ap-southeast-1 | international | `bailian-singapore-cs.alibabacloud.com` | `IntlBroadScopeAspnGateway` |

  未知 region 按 cn-beijing 处理。
- 请求：`POST https://<网关>/cli/api.json?action=<action>&product=sfm_bailian&api=<url 编码的 api>`
  - api = `zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage`
  - 头：`Authorization: Bearer <token>`、`Content-Type: application/x-www-form-urlencoded`、`Accept: */*`
  - 表单 body 两个字段：`region=<console_region>`；`params=<JSON 字符串>`，JSON 为
    `{"Api":"<api>","V":"1.0","Data":{"cornerstoneParam":{"protocol":"V2","console":"ONE_CONSOLE","productCode":"p_efm","switchUserType":3,"consoleSite":"BAILIAN_ALIYUN"}}}`
    （配置里有 `console_switch_agent` 时在 cornerstoneParam 里加 `"switchAgent": <值>`）
- 响应：
  - HTTP 非 2xx → 失败，错误里带前 500 字节的响应体。
  - `data.success == false` 且有 `data.errorCode`：errorCode 含 `NotLogined` → **token 失效**，其他 → 一般错误。
  - 成功时按顺序拆信封：取 `data`；`data.DataV2` 存在则取 `DataV2.data.data`，没有就取 `DataV2.data`，再没有就取 `DataV2`；
    `DataV2` 不存在则取 `data.data`，没有就取 `data`。拆出来的对象交给现有 `parseTokenPlan` 的字段
    （`per5HourPercentage` / `per1WeekPercentage` / `per1MonthPercentage` 及对应 `*ResetTime`）。

## 要做的事

### 1. 凭据来源

- `providers.Options` 增加 `AccessKeyID`、`AccessKeySecret` 两个字段；`config.ProviderCfg` 增加对应的
  `access_key_id`、`access_key_secret`，合并逻辑和 `api_key` 一样；`cmd/liquoten/main.go` 只做透传。
- 百炼 provider 取 AK/SK 的顺序：liquoten 配置 → `~/.bailian/config.json`（`bl` 的配置，字段 `access_key_id`、
  `access_key_secret`、`console_region`、`console_site`、`console_switch_agent`）。读 `bl` 配置的路径要能在测试里替换。
  这样用户用 `bl auth login --open-api` 配好 AK/SK 后，liquoten 不用再配一遍。
- region/site 从 `~/.bailian/config.json` 读，缺省 `cn-beijing` / `domestic`。

### 2. Query 分流

- 有 AK/SK → 直连；否则 → 现有 `bl` 路径，代码和行为都不动。
- 直连时 token 只缓存在内存里（不写回任何文件）：没有 token 时先换一个；查用量遇到 `NotLogined` 就重新换一次 token
  并重试**一次**，再失败就报错。不要每次轮询都换 token。
- 直连失败的错误信息要能看出是哪一步失败（换 token / 查用量），并且**绝不能**包含 AK secret 或 token 全文。
- 直连成功时 Sections 的生成逻辑复用现有 `sections()`。
- 直连模式下 `Preflight` 不应因为没装 `bl` 而失败（有 AK/SK 时直接返回成功；返回的 command 保持原配置值即可）。
- HTTP 用标准库 `net/http`，超时沿用 provider 的 timeout；base URL 要能在测试里替换成 `httptest.Server`。

### 3. 测试（同包 `_test.go`，fixture 放 `testdata/`）

- 签名固定向量（见上）。
- 网关选择表的 4 种组合 + 未知 region。
- 用 `httptest.Server` 跑通完整直连：换 token → 查用量 → 解析出 Sections；断言请求的 path、关键头、表单里的 `params` JSON 和 `region`。
- 用量响应 fixture 至少两份：`DataV2` 信封和 `data.data` 信封（字段名照上面，数值自拟）。
- `NotLogined` → 重新换 token → 重试成功；重试仍 `NotLogined` → 报错且只重试一次。
- token 缓存：连续两次 Query 只换一次 token。
- 换 token 失败（`Success:false`、`cliAccessToken` 为空）的报错，并断言错误信息里没有 secret。
- 没有 AK/SK 时仍走 `bl`（可复用现有 `testdata/fake-bl.sh`）。
- 从 `~/.bailian/config.json` 读 AK/SK 与 region/site（用临时文件）。

### 4. 文档

- `config.example.toml` 百炼段落加上 `access_key_id` / `access_key_secret`（注释掉），说明两种配置方式、
  建议用只开百炼只读权限的 RAM 子账号，以及配置文件里是明文。
- README 百炼相关的安装/配置段落补一小段：有 AK/SK 时不需要 `bl`，也不会因为 token 过期要重新登录。
- `bailian.go` 包注释现在写着“避免直接调用未公开的控制台网关接口”，改成说明两种模式。

## 验收

```bash
go test ./... && go vet ./... && test -z "$(gofmt -l .)"
```

全部通过，并按 AGENTS.md 提交一次 commit（信息贴合仓库既有风格，中文，一句话说清改了什么）。
