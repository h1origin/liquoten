# 任务 04：Codex 每小时用实时接口校正额度，每 12 小时查重置卡并提醒过期

## 背景

Codex provider 现在只读 `~/.codex/sessions` 会话日志里的 `rate_limits`。用户几天不用 CLI 时，日志里的数据会过时
（例如日志显示周额度 0%，实际已用 53%）。另外 Codex 会赠送“重置卡”（rate limit reset credit），有过期时间，
日志里没有这项数据。

Codex CLI（0.162.1）内部用 ChatGPT 后端接口拿到这两项数据，已经用真实账号验证过（只读 GET）。

开始前必须先读仓库根目录的 `AGENTS.md`，所有硬性规则都适用（分层、只用标准库、fixture 单测、gofmt/vet、每次改动后 commit）。

## 范围

- Go：`internal/providers/codex/`、`internal/alert/`。`cmd/liquoten/main.go` 原则上不用改。
- Swift：`app/AppDelegate.swift`（`sectionTitle`）、`app/Dashboard.swift`（Codex 的 `providerDescription`）。
- 不引入第三方依赖，不改其他 provider 的行为。

## 接口细节（已真机验证）

公共部分：

- 凭据：每次请求前**重新读** `<codex home>/auth.json`（`codex home` 就是现有的 `DefaultHome()`，测试里指向临时目录），
  取 `tokens.access_token` 和 `tokens.account_id`。文件不存在、或没有 `access_token`（例如 API Key 登录），就跳过实时查询，不报错。
- **不要**用 `refresh_token` 自己续期，也不要写回 `auth.json`：续期会让 Codex 本身掉线。令牌由 Codex 自己维护。
- 请求头：`Authorization: Bearer <access_token>`、`ChatGPT-Account-Id: <account_id>`（为空就不带）、`Accept: application/json`、
  `User-Agent: liquoten`。
- base URL：`https://chatgpt.com/backend-api`，要能在测试里替换成 `httptest.Server`。超时用 provider 的 timeout（工厂里接 `Options.Timeout`，<=0 时 20 秒）。
- 错误信息里**绝不能**出现 access_token 或 account_id 全文，也不能出现响应里的 email、user_id。

### 1. 实时额度：`GET /wham/usage`

响应示例（字段节选；fixture 自拟数值，不要用真实的 id/email）：

```json
{
  "plan_type": "pro",
  "rate_limit": {
    "allowed": true,
    "limit_reached": false,
    "primary_window": {"used_percent": 53, "limit_window_seconds": 604800, "reset_after_seconds": 501749, "reset_at": 1793781560},
    "secondary_window": null
  },
  "credits": {"has_credits": true, "unlimited": false, "balance": "62500.0000000000"},
  "rate_limit_reset_credits": {"available_count": 2, "applicable_available_count": 0}
}
```

- 映射到现有的 `rateLimits`/`window`：`primary_window` → Primary，`secondary_window` → Secondary；
  `used_percent` 照搬，`limit_window_seconds / 60` → `WindowMinutes`，`reset_at` → `ResetsAt`；`credits` 字段名一致。
  然后复用现有 `sections()` 生成 Sections，名字保持 `5-hour quota` / `1-week quota` / `credits` 不变。

### 2. 重置卡：`GET /wham/rate-limit-reset-credits`

```json
{
  "credits": [
    {"id": "RateLimitResetCredit_aaa", "reset_type": "codex_rate_limits", "is_supported_by_plan": true,
     "status": "available", "granted_at": "2026-09-29T19:09:27.129540Z", "expires_at": "2026-10-29T19:09:27.129540Z",
     "redeemed_at": null, "title": "Full reset"}
  ],
  "available_count": 2
}
```

- 只统计 `status == "available"`、`is_supported_by_plan == true`、并且 `expires_at` 晚于当前时间的卡。
- 同一路径下还有 `/wham/rate-limit-reset-credits/consume`（使用重置卡）。**任何代码和测试都不能请求它**，
  测试里的 httptest handler 收到 consume 请求要直接 `t.Fatal`。

## 要做的事

### 1. 额度：本地日志优先，每小时用实时数据校正

- `Query` 照旧先读本地日志。
- 实时额度最多 **每 1 小时**请求一次 `/wham/usage`：第一次 `Query` 时立即请求，之后距离上次**尝试**满 1 小时再请求（失败也算一次尝试，不要失败后每分钟重试）。
  结果和请求时间缓存在内存里，不写文件。
- 选数据：本地日志事件的时间戳和实时数据的请求时间，**谁新用谁**来生成额度 Sections（包括 credits 余额）。
  `Balance.FetchedAt` 用被选中那份数据的时间。
- 本地日志没有数据、但有实时数据时，用实时数据，不再报“尚无数据”。两边都没有时，保持现有错误信息。
- 实时请求失败（网络错误、401/403、解析失败）时：**不影响** Query 结果（仍返回本地数据），也不把错误放进 `Balance`；
  只在 stderr 打一行简短日志（不含令牌）。

### 2. 重置卡：每 12 小时查一次

- `/wham/rate-limit-reset-credits` 最多 **每 12 小时**请求一次，规则同上（首次立即请求，失败也算尝试），结果缓存在内存里。
- 有成功的结果时，Sections 末尾追加一项：
  `Section{Name: "reset-credits", UsedPct: -1, Remaining: <可用张数>, ResetAt: <最早过期时间>}`。
  可用张数和最早过期时间在每次 `Query` 时按当前时间重新计算（已过期的卡不算）；张数为 0 时 `ResetAt` 为零值，仍然显示 0 张。
- 从来没有成功查到过时不加这一项。

### 3. 过期提醒（`internal/alert/`）

- 在 `Engine.Check` 里加一条规则：遇到名为 `reset-credits` 的 Section，`Remaining > 0`、`ResetAt` 非零，并且
  `ResetAt - now < 12 小时` 时，发一次通知。
  - 标题 `额度提醒`，内容如 `Codex 有重置卡将于 03:09 过期，可在 Codex 桌面 App 里使用`（时间格式复用 `message` 里“当天只显示时分，否则显示月日时分”的写法，可以抽成一个小函数）。
  - 去重键 `provider|reset-credits|<ResetAt 的 Unix 秒>`，存进现有的 `alerted` 并持久化：同一张卡只提醒一次，daemon 重启也不重复；
    下一张卡的过期时间不同，到时会再提醒。
  - 这条规则是按每次轮询的缓存数据判断的，所以会在过期前 12 小时左右准时提醒，不必等下一次 12 小时查询。
- `sectionName` 增加 `reset-credits` → `重置卡`。

### 4. App（Swift）

- `AppDelegate.sectionTitle` 增加 `"reset-credits": "重置卡"`。它的 `usedPct < 0 && remaining >= 0`，会自动作为 Chip 显示在 Codex 标题行，形如 `重置卡 2`，不需要额外改布局。
- Codex 的 `providerDescription` 改为：
  `读取 Codex 本地会话记录（~/.codex/sessions）里的订阅用量和 credits 余额，并用 Codex 的登录状态每小时校正一次、每 12 小时查询一次重置卡。无需额外配置。`

### 5. 测试（同包 `_test.go`，fixture 放 `testdata/`）

用 `httptest.Server` + 注入的 `now`：

- 实时额度映射：fixture → 正确的 Sections（周额度百分比、重置时间、credits）。
- 本地日志比实时数据新时用本地，实时数据新时用实时；本地日志为空时用实时数据。
- 频率：1 小时内连续多次 `Query` 只请求一次 `/wham/usage`；时间前进 1 小时后再请求一次；重置卡同理（12 小时）。
- 失败：401 时 `Query` 仍返回本地数据且无错误，并且 1 小时内不重试。
- 断言请求头带 `Authorization` 和 `ChatGPT-Account-Id`；`auth.json` 改了令牌后，下一次请求用的是新令牌。
- 没有 `auth.json` / 没有 access_token：不发请求，行为和现在一致（现有测试全部继续通过）。
- 重置卡：过滤掉非 available、已过期的卡；张数和最早过期时间正确；时间推进越过最早过期时间后，张数减一、ResetAt 变成下一张的。
- 任何请求 `/consume` 的情况都会让测试失败。
- alert：`reset-credits` 距离过期 13 小时不提醒、11 小时提醒一次、再次 Check 不重复、重建 Engine（读持久化状态）也不重复；ResetAt 换成另一张时再提醒。
- 错误信息里不含令牌（构造一个会把响应体带进错误的失败场景来断言）。

## 验收

```bash
go test ./... && go vet ./... && test -z "$(gofmt -l .)" && ./app/build.sh
```

全部通过，并按 AGENTS.md 提交一次 commit（本任务说明一起提交，`build/` 不提交）。
