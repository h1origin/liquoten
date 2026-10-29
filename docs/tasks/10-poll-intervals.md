# 任务 10：统一远端查询频率

## 背景

用户决定：

- 所有请求远端接口的**实时额度**一律 **30 分钟**一次；
- Codex 重置卡明细、百炼额度补充、百炼套餐信息一律 **6 小时**一次；
- Codex 重置卡离过期**不到 12 小时**时提醒一次（现有规则，保持不变，确认测试覆盖即可）。

本地读取（Claude Code 快照、Codex 本地会话日志）不变，仍跟全局间隔（默认 1 分钟）。

开始前必须先读仓库根目录的 `AGENTS.md`，所有硬性规则都适用。本任务在任务 09 之后做。

## 要改的

| 位置 | 现在 | 改为 |
|---|---|---|
| Codex 实时额度 `/wham/usage`（`usageInterval`） | 1 小时 | 30 分钟 |
| Codex 重置卡 `/wham/rate-limit-reset-credits`（`resetCreditsInterval`） | 12 小时 | 6 小时 |
| 百炼用量（`PollInterval()`） | 10 分钟 | 30 分钟 |
| 百炼额度补充、套餐（`extrasInterval`） | 12 小时 | 6 小时 |
| Qoder / Qoder CN（`PollInterval()`） | 5 分钟 | 30 分钟 |

- Codex 每次 `/wham/usage` 时用 `available_count` 校正重置卡的逻辑不变（随之变成每 30 分钟核对一次）。
- 刷新信号（SIGUSR1）的行为不变：立即查询，并让上面这些长周期缓存立即重查。
- Codex 重置卡过期提醒窗口 `resetCreditsAlertWindow` 保持 12 小时。

## 同步文案

- `app/Dashboard.swift` 的 `providerDescription`：
  - 百炼：`用阿里云 AccessKey 查询 Token Plan 用量（每 30 分钟）、额度补充和套餐到期时间（每 6 小时）。建议使用只授予百炼权限的 RAM 子账号。`
  - Codex：`读取 Codex 本地会话记录（~/.codex/sessions）里的订阅用量和 credits 余额，并用 Codex 的登录状态每 30 分钟校正一次、每 6 小时查询一次重置卡。无需额外配置。`
  - Qoder / Qoder CN：在现有说明里注明每 30 分钟查询一次。
- `README.md`、`README.zh-CN.md` 里提到这些频率的地方（Codex 的 hourly / 每小时、12 小时等）同步改掉。
- 代码注释里提到旧频率的（例如 qoder.go 的“5 分钟查一次足够”）同步改掉。

## 测试

- 现有频率相关测试里的时间推进改成新的间隔（30 分钟 / 6 小时），断言“间隔内不重复请求、到点再请求、失败也算一次尝试”仍然成立。
- 各 provider 的 `PollInterval()` 返回值有断言。
- Codex 重置卡：离过期 13 小时不提醒、11 小时提醒一次（已有就保留）。

## 验收

```bash
go test ./... && go vet ./... && test -z "$(gofmt -l .)" && ./app/build.sh
```

全部通过，并按 AGENTS.md 提交一次 commit（本任务说明一起提交，`build/` 不提交）。

## 附加：百炼月额度用满后，额度补充改为 30 分钟查一次

套餐月额度用完后才开始扣用量包，此时用量包剩余变化快，6 小时太慢。

- 每次 `Query` 拿到用量后，如果 `1-month quota` 的已用百分比 **≥ 100**，额度补充（`addon/summary`）的查询间隔用 **30 分钟**；否则仍是 6 小时。
  套餐信息（`subscription`）始终 6 小时。
- 判断用本次刚查到的月额度，不用上一次的；从 <100 变成 ≥100 的那一次 Query，如果距上次额度补充查询已满 30 分钟，就在同一次 Query 里立即查。
- 月额度重置回 <100 后恢复 6 小时。
- 测试：月额度 99.9% 时 30 分钟后不重查额度补充；100% 时 30 分钟后重查；套餐信息在两种情况下都不受影响。
- 百炼 `providerDescription` 的说明补一句：`月额度用满后额度补充改为每 30 分钟查询。`
