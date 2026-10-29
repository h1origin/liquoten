# 任务 06：百炼显示额度补充（用量包）和套餐到期，并提醒；用量改为 10 分钟查一次

## 背景

百炼控制台的 Token Plan 页面还有“额度补充”（加购的用量包）和套餐到期信息。已用真实账号验证：
用现有的 AK/SK 换来的 token、现有的控制台网关调用方式（`fetchUsage` 那一套：同一个 endpoint、同样的 form 字段 `region` + `params`，
只把 `Api` 换掉），可以查到下面两个接口。

开始前必须先读仓库根目录的 `AGENTS.md`，所有硬性规则都适用。

## 范围

- Go：`internal/providers/bailian/`、`internal/alert/`、`cmd/liquoten/main.go`（只改轮询间隔的取法，见第 4 点）。
- Swift：`app/AppDelegate.swift`（`sectionTitle`）、`app/Dashboard.swift`（百炼 `providerDescription`）。
- 不改其他 provider 的代码。

## 接口（已真机验证，响应信封与 usage 相同：`data.DataV2.data.data`）

1. 额度补充汇总：`Api = zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/addon/summary`

   ```json
   {"remainingCredits": 20000.0, "activeCount": 1, "totalCredits": 20000.0, "nearestExpireTime": 1795536000000}
   ```

   时间是毫秒时间戳。没有生效中的用量包时字段可能缺失或为 0，要能处理。

2. 套餐信息：`Api = zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/subscription`

   ```json
   {"instanceCode": "sfm_tokenplansolo_public_cn-xxxx", "specCode": "standard", "remainingDays": 15,
    "startTime": 1786610148000, "endTime": 1794585600000, "autoRenewFlag": false, "status": "VALID"}
   ```

- 同一前缀下还有 `reset-card/use`（使用重置卡）等写操作接口：**任何代码和测试都不能调用**除上面两个和现有 `usage` 以外的接口；
  测试里的 httptest handler 收到其他 `Api` 要 `t.Fatal`。
- 抽一个通用的“按 Api 调网关并拆信封”的函数，`usage`、`addon/summary`、`subscription` 共用；`NotLogined` 换 token 重试一次的逻辑三者一致。
- fixture 放 `testdata/`，数值自拟，不要用真实的 instanceCode / orderId。

## 要做的事

### 1. 查询频率

- 百炼 provider 实现 `providers.Scheduled`，`PollInterval()` 返回 **10 分钟**（用量）。
- `addon/summary` 和 `subscription` 最多 **每 12 小时**请求一次：第一次 `Query` 立即请求，之后距离上次**尝试**满 12 小时再请求，失败也算尝试。
  结果缓存在内存里。这两个请求失败不影响 `Query` 结果（仍返回用量），只在 stderr 打一行日志（不含 token、AK）。

### 2. 额度补充 Section

有成功结果且 `totalCredits > 0` 时，Sections 末尾追加：

```go
Section{Name: "addon-credits", UsedPct: (total-remaining)/total*100, Remaining: remaining, ResetAt: nearestExpireTime}
```

- 每次 `Query` 用当前时间判断：`nearestExpireTime` 已过就不再追加（等下一次 12 小时查询刷新）。
- 它有 UsedPct，会作为一张额度卡片显示；alert 引擎会按通用的剩余 50%/20% 规则提醒，这是期望行为。

### 3. 套餐到期 Section

有成功结果时追加：

```go
Section{Name: "subscription", UsedPct: -1, Remaining: <剩余天数>, ResetAt: endTime}
```

- 剩余天数按当前时间和 `endTime` 现算（向上取整，不小于 0），不要直接用接口的 `remainingDays`（它只在查询那一刻准确）。
- `UsedPct < 0 && Remaining >= 0`，会作为 Chip 显示在百炼标题行，形如 `套餐剩余 15 天`（标题见第 6 点）。

### 4. 轮询间隔（`cmd/liquoten/main.go`）

现在 `every()` 只在 provider 的 `PollInterval()` **小于**全局间隔时才生效，所以更长的间隔不起作用（Qoder 声明的 5 分钟实际上也没生效）。
改为：provider 实现了 `Scheduled` 且 `PollInterval() > 0` 时就用它，否则用全局间隔。补一个单测覆盖（可以把 `every` 抽成小函数测）。
这会让 Qoder 按它声明的 5 分钟查询，属于预期修正。

### 5. 提醒（`internal/alert/`）

在现有 `reset-credits` 规则旁边加两条，写法一致（去重键持久化、同一个时间点只提醒一次、重启不重复）：

- `addon-credits`：`Remaining > 0` 且 `ResetAt - now < 3 天` 时提醒一次：
  `百炼额度补充将于 11月25日 00:00 过期，剩余 20000 Credits`。去重键 `provider|addon-credits-expire|<ResetAt Unix>`。
  注意 `addon-credits` 有 UsedPct，**同时**还要走通用的剩余百分比提醒，两者互不影响。
- `subscription`：`ResetAt - now < 3 天` 且未过期时提醒一次：`百炼 Token Plan 套餐将于 11月14日 00:00 到期`。
  去重键 `provider|subscription|<ResetAt Unix>`。
- 时间格式复用 `formatTime`。`sectionName` 增加 `addon-credits` → `额度补充`、`subscription` → `套餐`。
- 3 天写成常量。

### 6. App（Swift）

- `AppDelegate.sectionTitle`：`"addon-credits": "额度补充"`、`"subscription": "套餐剩余"`。
- `subscription` 的 Chip 要显示成 `套餐剩余 15 天`：在 `ProviderHeader` 里对这个 name 特殊处理，加上“天”字，其他 Chip 不变。
- 额度补充卡片：确认现有 `SectionCard` 对未知名字（非 5h/周/月）能正常显示百分比、用量条和重置时间，走势图可以不画或按现有默认逻辑；
  如果有崩溃或明显错位再做最小修改。“重置”字样对用量包不准确：卡片上 `addon-credits` 的时间文案改为“到期”。
- 百炼 `providerDescription` 改为：
  `用阿里云 AccessKey 查询 Token Plan 用量（每 10 分钟）、额度补充和套餐到期时间（每 12 小时）。建议使用只授予百炼权限的 RAM 子账号。`

## 测试（同包 `_test.go`）

- 两个新接口的 fixture 解析；请求里 `params` 的 `Api` 正确；只会请求三个允许的 Api。
- 12 小时频率：12 小时内多次 Query 只请求一次；时间推进后再请求；失败后 12 小时内不重试；失败时 Query 仍返回用量。
- `addon-credits` 的 UsedPct / Remaining / ResetAt 正确；total 为 0 或过期后不追加。
- `subscription` 剩余天数随 now 变化正确（向上取整、过期为 0）。
- `PollInterval()` 为 10 分钟；main 的 `every` 新规则。
- alert：额度补充和套餐到期在 3 天外不提醒、3 天内提醒一次、重复 Check 和重建 Engine 不重复、换一个到期时间再提醒；
  额度补充的剩余百分比提醒仍然照常触发。
- 错误信息、日志里没有 token 和 AK secret。

## 验收

```bash
go test ./... && go vet ./... && test -z "$(gofmt -l .)" && ./app/build.sh
```

全部通过，并按 AGENTS.md 提交一次 commit（本任务说明一起提交，`build/` 不提交）。
