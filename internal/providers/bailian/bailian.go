// Package bailian 实现阿里云百炼(Token Plan)额度查询。
//
// 通过执行官方开源 CLI(bailian-cli)获取 JSON 输出，把 CLI 当作稳定的
// client library，避免直接调用未公开的控制台网关接口。
//
// Token Plan 的余量语义是滚动配额百分比：5 小时窗口 + 1 周窗口的已用百分比
// 及重置时间，与官方控制台展示一致，没有"绝对剩余 credits"接口。
package bailian

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lk/liquoten-cred/internal/providers"
)

// Name 是注册表里的 provider 名称。
const Name = "bailian"

const defaultTimeout = 45 * time.Second

// Provider 执行 `bl usage token-plan --output json` 并解析结果。
type Provider struct {
	command string
	apiKey  string
	timeout time.Duration
}

func init() {
	providers.Register(Name, func(opts providers.Options) (providers.Provider, error) {
		return New(opts), nil
	})
}

// New 构造百炼 provider。opts.Command 留空时使用 PATH 中的 `bl`。
func New(opts providers.Options) *Provider {
	cmd := opts.Command
	if cmd == "" {
		cmd = "bl"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Provider{command: cmd, apiKey: opts.APIKey, timeout: timeout}
}

// Name 返回注册表名称。
func (p *Provider) Name() string { return Name }

// Query 查询 Token Plan 的 5 小时 / 1 周窗口用量。
func (p *Provider) Query(ctx context.Context) (providers.Balance, error) {
	bal := providers.Balance{Provider: Name, FetchedAt: time.Now()}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	out, err := p.run(ctx, "usage", "token-plan", "--output", "json")
	if err != nil {
		return bal, fmt.Errorf("查询 Token Plan 用量失败: %w", err)
	}
	tp, err := parseTokenPlan(out)
	if err != nil {
		return bal, fmt.Errorf("解析 token-plan 输出失败: %w", err)
	}

	bal.Sections = []providers.Section{
		{Name: "5-hour quota", UsedPct: pctOrMinus1(tp.Per5Hour), Remaining: -1, ResetAt: epochMs(tp.Per5HourReset)},
		{Name: "1-week quota", UsedPct: pctOrMinus1(tp.Per1Week), Remaining: -1, ResetAt: epochMs(tp.Per1WeekReset)},
	}
	return bal, nil
}

// run 执行 CLI 并返回 stdout；失败时把 stderr 带进错误信息。
func (p *Provider) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, p.command, args...)
	cmd.Env = os.Environ()
	if p.apiKey != "" {
		cmd.Env = append(cmd.Env, "DASHSCOPE_API_KEY="+p.apiKey)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s %s: %s", p.command, strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// tokenPlan 对应 `bl usage token-plan --output json` 的输出字段。
// API 返回的百分比是 [0,1] 比值，重置时间是毫秒 epoch。
type tokenPlan struct {
	Per5Hour      *float64 `json:"per5HourPercentage"`
	Per5HourReset *int64   `json:"per5HourResetTime"`
	Per1Week      *float64 `json:"per1WeekPercentage"`
	Per1WeekReset *int64   `json:"per1WeekResetTime"`
}

// parseTokenPlan 兼容两种形态：直接对象，或 {"data": {...}} 信封。
// 字段缺失（无限额）时指针为 nil，由上层转成 UsedPct=-1。
func parseTokenPlan(raw []byte) (tokenPlan, error) {
	var tp tokenPlan
	if err := json.Unmarshal(raw, &tp); err != nil {
		return tp, err
	}
	if tp.Per5Hour == nil && tp.Per1Week == nil {
		var envelope struct {
			Data tokenPlan `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return tp, err
		}
		tp = envelope.Data
	}
	return tp, nil
}

// pctOrMinus1 归一化为百分比：API 约定是 [0,1] 比值，>1 时视为已是百分数；缺失/负值返回 -1。
func pctOrMinus1(v *float64) float64 {
	if v == nil {
		return -1
	}
	x := *v
	if x < 0 {
		return -1
	}
	if x <= 1 {
		x *= 100
	}
	return x
}

// epochMs 把时间戳转为 time.Time：毫秒直接转，秒级(<1e12)自动乘 1000；nil/0 返回零值。
func epochMs(v *int64) time.Time {
	if v == nil || *v == 0 {
		return time.Time{}
	}
	ms := *v
	if ms < 1e12 {
		ms *= 1000
	}
	return time.UnixMilli(ms)
}
