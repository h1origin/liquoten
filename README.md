# liquoten-cred

macOS 菜单栏实时显示 LLM 额度/余额的轻量监控工具。只读轮询各 provider 的余额接口，不站在你的请求链路上。

![架构图](docs/liquoten-cred-architecture.png)

> 架构图为可编辑的 Excalidraw 源文件：[docs/liquoten-cred-architecture.excalidraw](docs/liquoten-cred-architecture.excalidraw)（拖入 excalidraw.com 或 VS Code 插件编辑）

- **当前支持**：阿里云百炼 Token Plan（5 小时 / 1 周滚动配额百分比 + 重置倒计时）
- **架构**：Go daemon（launchd 常驻）轮询 → 写 `~/.cache/liquoten-cred/state.json` → SwiftBar 插件渲染菜单栏；阈值升穿发 macOS 系统通知
- **通用性**：新增 provider = 实现 `providers.Provider` 接口 + 一行注册，展示层无需改动

## 工作原理

```
bailian-cli (官方 CLI)  →  daemon 每 10min 轮询  →  state.json / history.jsonl
                                                 ↘ 阈值升穿 → osascript 通知
state.json  ← SwiftBar 插件只读渲染菜单栏
```

一次轮询：对每个启用的 provider 调 `Query()`，结果落盘缓存（原子写）并追加历史，然后过阈值状态机（升穿发一次通知，回落复位，重启后状态不丢）。

## 安装

前置：Go ≥ 1.21（`brew install go`，go.mod 会自动拉取 1.27 工具链）、Node ≥ 22.12。

```bash
# 1. 安装官方百炼 CLI（liquoten-cred 的百炼 adapter 通过它查询）
npm install -g bailian-cli
bl auth login --api-key sk-你的key        # 或 export DASHSCOPE_API_KEY=sk-xxx 写入 ~/.zshrc

# 2. 构建
cd ~/code/liquoten-cred
mkdir -p ~/.local/bin
go build -o ~/.local/bin/liquoten-cred ./cmd/liquoten-cred
go build -o ~/.local/bin/liquoten-cred-bar ./swiftbar

# 3. 配置（可选，默认配置即可跑）
mkdir -p ~/.config/liquoten
cp config.example.toml ~/.config/liquoten/config.toml

# 4. 先手动验证一次（详见下方"验证"）
~/.local/bin/liquoten-cred -once

# 5. 常驻（开机自启）。plist 里的路径是硬编码的，换机器先替换：
sed -i '' "s|/Users/lk|$HOME|g" launchd/com.liquoten-cred.plist
cp launchd/com.liquoten-cred.plist ~/Library/LaunchAgents/
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.liquoten-cred.plist
# 旧版 macOS 用 launchctl load ~/Library/LaunchAgents/com.liquoten-cred.plist
```

## SwiftBar 菜单栏

```bash
brew install --cask swiftbar
# 启动 SwiftBar 后在设置里选择插件目录，然后：
ln -s ~/.local/bin/liquoten-cred-bar "$SWIFTBAR_PLUGIN_DIR/liquoten-cred.10m.sh"
```

插件文件名后缀控制 SwiftBar 的刷新间隔：`liquoten-cred.10m.sh` = 每 10 分钟执行一次（与 daemon 轮询周期一致即可）。插件本身只是读 state.json 输出 xbar 文本，无网络调用，执行很快。

菜单栏标题形如 `百炼 5h 37% · 周 12%`，多 provider 用 `·` 连接。颜色语义：

| 位置 | 白色 | 橙色 | 红色 |
|---|---|---|---|
| 菜单栏标题 | 正常 | 任一窗口超阈值 | 有 provider 查询失败 |
| 下拉详情分项 | 正常 | 已用 ≥ 70%（接近） | 已用 ≥ 阈值 |

下拉菜单包含：各窗口百分比与重置倒计时（`（3h 20m 后重置）`）、最后更新时间、打开百炼控制台链接、手动刷新。daemon 未运行时显示灰色 `liquoten-cred 未运行`。

## 配置

见 [config.example.toml](config.example.toml)。默认阈值：5 小时窗口 80%、周窗口 90%，升穿时发一次系统通知，回落后复位（下次升穿再告警）。

环境变量：

| 变量 | 作用 |
|---|---|
| `DASHSCOPE_API_KEY` | 百炼 API key（bailian-cli 读取，与 `bl auth login` 二选一） |
| `LIQUOTEN_CONFIG` | 覆盖默认配置文件路径（`~/.config/liquoten/config.toml`） |
| `LIQUOTEN_CRED_CACHE_DIR` | 覆盖默认缓存目录（`~/.cache/liquoten-cred`） |

daemon flags：`-config`（配置文件）、`-once`（只轮询一次）、`-cache-dir`（缓存目录）。

## 验证

```bash
go test ./... && go vet ./... && test -z "$(gofmt -l .)"   # 单测 + 静态检查

~/.local/bin/liquoten-cred -once                                  # 跑一轮
cat ~/.cache/liquoten-cred/state.json                             # 与控制台百分比对比
```

手工场景：把 `config.toml` 阈值临时调低（如 10%）再 `-once`，应弹系统通知；临时 `mv` 掉 bl 的路径或断网，菜单栏应变红色错误态。单测覆盖：百炼 JSON 解析（录制 fixture，CLI 输出格式变了测试先红）、阈值状态机（含重启持久化）、store 原子写回读、config 合并。

## 扩展新 provider

1. 新建 `internal/providers/<name>/<name>.go`，实现 `providers.Provider` 接口并在 `init()` 里 `providers.Register`
2. 在 `cmd/liquoten-cred/main.go` 里 `import _` 该包（注册入口）
3. 配置里加 `[providers.<name>]` 与 `[thresholds.<name>]`

各 provider 的余额语义不同，统一归一化为 `Section{Name, UsedPct, Remaining, ResetAt}`（无对应语义的字段置 -1）：

| Provider | 查询接口 | 语义 |
|---|---|---|
| 百炼 Token Plan | 官方 CLI `bl usage token-plan --output json` | 5h/周窗口已用百分比 |
| OpenAI / Codex | `GET api.openai.com/v1/dashboard/billing/subscription` | hard/soft limit USD；ChatGPT 订阅套餐无接口 |
| Anthropic / Claude Code | `GET api.anthropic.com/v1/organizations/cost_calendar` | 本月累计费用 |
| DeepSeek | `GET api.deepseek.com/user/balance` | 剩余 CNY |
| OpenRouter | `GET openrouter.ai/api/v1/credits` | 剩余 credits |

（表内非百炼接口为设计期核对的接入点，实现时需用真实 key 再验证。）

## 项目结构

```
cmd/liquoten-cred/           daemon 入口（轮询循环、launchd 常驻）
internal/config/       配置加载（TOML + 环境变量）
internal/providers/    Provider 接口 + 注册表
  bailian/             百炼 adapter（exec bailian-cli + fixture 单测）
internal/store/        state.json 原子写 + history.jsonl 追加
internal/alert/        阈值状态机 + osascript 通知
swiftbar/              菜单栏插件（xbar 协议输出）
launchd/               com.liquoten-cred.plist
docs/                  架构图（.excalidraw 可编辑源文件 + PNG 预览）
```

## 故障排查

| 症状 | 处理 |
|---|---|
| 菜单栏灰色 `liquoten-cred 未运行` | daemon 没起来：`launchctl list \| grep liquoten-cred`；看日志 `tail ~/Library/Logs/liquoten-cred.log` |
| `bl: command not found` | `npm install -g bailian-cli`（Node ≥ 22.12）；或在配置里 `command = "/绝对路径/bl"` |
| 超阈值但不弹通知 | osascript 通知需授权：系统设置 → 通知里允许对应应用；勿扰模式会吞掉通知 |
| 百分比与控制台不一致 | 有最多一个轮询间隔的延迟；`liquoten-cred -once` 手动刷新后对比 |
| 重启后没自动起来 | `launchctl list \| grep liquoten-cred` 应有进程；plist 路径写错时 `launchctl bootout` 后改路径重装 |

## 卸载

```bash
launchctl bootout gui/$(id -u)/com.liquoten-cred    # 旧版 macOS: launchctl unload ~/Library/LaunchAgents/com.liquoten-cred.plist
rm ~/Library/LaunchAgents/com.liquoten-cred.plist
rm ~/.local/bin/liquoten-cred ~/.local/bin/liquoten-cred-bar
rm -rf ~/.cache/liquoten-cred ~/.config/liquoten     # 缓存与配置（含历史数据）
```

## 设计与边界

- **为什么走官方 CLI**：百炼 Token Plan 的额度接口是控制台内部网关（未公开），官方开源 CLI 是稳定的封装，Go 侧 exec 它拿 JSON，避免直连脆弱的内部接口
- **Token Plan 没有"绝对剩余 credits"**：官方只暴露滚动窗口的已用百分比，菜单栏展示与官方控制台对齐
- **历史数据**：`~/.cache/liquoten-cred/history.jsonl` 每轮追加一行，10min 间隔约 144 行/天（v1 用 JSONL 而非 SQLite，零依赖；做趋势看板时再评估迁移）
- **失败降级**：查询连续失败 3 次告警一次；`bl` 未安装时 daemon 启动即报错并给出提示
- **Free tier**：百炼按模型粒度的免费额度查询留到 v1.1（数据在 `bl usage free --output json`，结构已核实）

## 路线图

- **v1.1**：百炼 free tier 按模型额度展示
- **v2**：OpenAI / Anthropic / DeepSeek / OpenRouter adapter（接口已核对，每个 1–2 小时）
- **v3**（可选）：本地网页 dashboard，读 history.jsonl 看趋势；届时再评估引入 SQLite

## 调试

```bash
liquoten-cred -once                 # 只轮询一次
tail -f ~/Library/Logs/liquoten-cred.log
cat ~/.cache/liquoten-cred/state.json
```
