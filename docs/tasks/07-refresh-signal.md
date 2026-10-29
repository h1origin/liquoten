# 任务 07：刷新不再重启 daemon；Codex 用实时接口里的张数校正重置卡；刷新按钮移到右上角

## 背景

1. 面板左下角“刷新”现在调用 `forceRefresh()` → `daemon.restart()`：杀掉 daemon 再拉起。副作用是内存缓存全丢
   （百炼 token、Codex 实时额度和重置卡、各种 1 小时 / 12 小时计时），每点一次都要多发一串请求。
2. Codex 重置卡 12 小时才查一次，用户用掉一张后最长 12 小时才更新。而每小时查一次的 `/wham/usage` 响应里
   本来就带 `rate_limit_reset_credits.available_count`。
3. 用户要求把刷新按钮放到面板右上角。

开始前必须先读仓库根目录的 `AGENTS.md`，所有硬性规则都适用。本任务在任务 06 之后做，基于任务 06 的代码。

## 要做的事

### 1. 领域接口（`internal/providers/provider.go`）

新增可选接口：

```go
// Refresher 由带长周期缓存的 provider 实现；RefreshNow 让下一次 Query 忽略这些缓存的间隔，立即重新请求。
type Refresher interface {
    RefreshNow()
}
```

- Codex：`RefreshNow` 把实时额度和重置卡的“上次尝试时间”清零。
- 百炼：`RefreshNow` 把额度补充和套餐的“上次尝试时间”清零（不清 token）。

### 2. daemon 收到 SIGUSR1 立即刷新（`cmd/liquoten/main.go`，只做装配）

- 用 `os/signal` 监听 `SIGUSR1`。收到后：对实现了 `Refresher` 的 provider 调 `RefreshNow()`，把所有 provider 的 `due` 设为现在，立即执行一轮 `poll`。
- 主循环现在是 `time.Sleep(time.Until(next))`，改成 `select` 同时等定时器和信号通道，信号来了马上醒。
- 防抖：距离上一次**信号触发**的轮询不到 10 秒的信号直接忽略（日志记一行），避免连点导致连发请求。
- 注意 `poll`、`due`、`latest` 只在主 goroutine 里访问，不要引入数据竞争（信号只通过 channel 传给主循环）。
- 补单测：把“收到信号后怎么处理 due 和 Refresher”抽成小函数测试；防抖逻辑用注入的 now 测试。

### 3. App（Swift）

- `DaemonManager` 新增 `refreshNow()`：daemon 在运行就 `kill(process.processIdentifier, SIGUSR1)`；没在运行就 `spawn()`。
- “刷新”按钮改为调用新的 `refreshNow()`，然后像现在一样立即 `refresh()` 一次、3 秒后再 `refresh()` 一次。
- 配置变化后的 `finish()` 里**仍然**重启 daemon（要重新读配置）：把现在的 `forceRefresh()` 改名为 `restartDaemon()` 之类，只给这些地方用。
- 刷新按钮从 `FooterBar` 移到 `HeaderView` 右上角：和“所有额度中最高已用”那一行同一行、靠右，只显示图标 `arrow.clockwise`（`.help("刷新")`），borderless 风格。
  点击后到 3 秒那次 `refresh()` 完成前，按钮禁用并让图标转动或换成小的 `ProgressView`，防止连点。
- `FooterBar` 去掉刷新按钮，其余按钮不变。
- README 两个语言版本里描述面板底部按钮和“刷新会重启 daemon”的地方同步改掉（如有）。

### 4. Codex 用实时接口的张数校正重置卡（`internal/providers/codex/`）

- 解析 `/wham/usage` 里的 `rate_limit_reset_credits.available_count`（缺失时视为未知，不做任何事）。
- 每次 `/wham/usage` 成功后，如果这个数和当前缓存算出来的可用张数（`availableResetCredits(..., now)` 的 count）不同，
  就把重置卡的“上次尝试时间”清零，让同一次 `Query` 里紧接着重新请求 `/wham/rate-limit-reset-credits`
  （`Query` 里已经是先 `refreshLiveUsage` 后 `refreshResetCredits`，保持这个顺序）。
- 还从来没成功查到过重置卡时不需要特殊处理（本来就会查）。
- 补单测：缓存 2 张、usage 返回 1 → 同一次 Query 重新请求重置卡；相等 → 不请求；字段缺失 → 不请求。仍然禁止请求 `/consume`。

## 验收

```bash
go test ./... && go vet ./... && test -z "$(gofmt -l .)" && ./app/build.sh
```

全部通过；再手动验证一次：用 `build/Liquoten.app/Contents/Resources/liquoten -cache-dir <临时目录> -config <只启用 codex 的临时配置>`
后台跑起来，`kill -USR1 <pid>` 后日志里能看到立即轮询，10 秒内第二次信号被忽略，然后结束进程（不要动用户真实的缓存目录和配置）。
按 AGENTS.md 提交一次 commit（本任务说明一起提交，`build/` 不提交）。

## 附加：百炼套餐剩余天数与控制台对齐

任务 06 的 `subscription` Section 剩余天数用了向上取整，实测 15 天 0 小时 12 分显示成 16，而控制台显示 15。
改为**向下取整**（与控制台一致），已过期为 0；同步改 `extras_test.go` 和任务 06 文档里“向上取整”的描述。
