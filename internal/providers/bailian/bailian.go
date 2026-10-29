// Package bailian 实现阿里云百炼(Token Plan)额度查询。
//
// 使用 AccessKey ID / Secret 签名调用 OpenAPI 换取 access token，再查控制台网关。
// Token Plan 的余量语义是滚动配额百分比：5 小时窗口 + 1 周窗口的已用百分比
// 及重置时间，与官方控制台展示一致，没有"绝对剩余 credits"接口。
package bailian

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

// Name 是注册表里的 provider 名称。
const Name = "bailian"

const defaultTimeout = 45 * time.Second

const pollInterval = 10 * time.Minute

// Provider 查询百炼 Token Plan 用量。
type Provider struct {
	accessKeyID             string
	accessKeySecret         string
	timeout                 time.Duration
	tokenBaseURL            string
	gatewayBaseURL          string
	now                     func() time.Time
	mu                      sync.Mutex
	token                   string
	lastAddonAttempt        time.Time
	addonSummary            addonSummaryResponse
	addonLoaded             bool
	lastSubscriptionAttempt time.Time
	subscription            subscriptionInfo
	subscriptionLoaded      bool
}

func init() {
	providers.Register(Name, func(opts providers.Options) (providers.Provider, error) {
		return New(opts), nil
	})
}

// New 构造百炼 provider。
func New(opts providers.Options) *Provider {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Provider{
		accessKeyID:     opts.AccessKeyID,
		accessKeySecret: opts.AccessKeySecret,
		timeout:         timeout,
		now:             time.Now,
	}
}

// Name 返回注册表名称。
func (p *Provider) Name() string { return Name }

// PollInterval 返回用量轮询间隔。
func (p *Provider) PollInterval() time.Duration { return pollInterval }

// Query 查询 Token Plan 的 5 小时 / 1 周窗口用量。
func (p *Provider) Query(ctx context.Context) (providers.Balance, error) {
	now := p.now()
	bal := providers.Balance{Provider: Name, FetchedAt: now}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	if !p.hasCredentials() {
		return bal, errors.New("未配置阿里云 AccessKey，请在服务页填写")
	}
	return p.queryDirect(ctx, bal, now)
}

func sections(tp tokenPlan) []providers.Section {
	windows := []struct {
		name  string
		pct   *float64
		reset *int64
	}{
		{"5-hour quota", tp.Per5Hour, tp.Per5HourReset},
		{"1-week quota", tp.Per1Week, tp.Per1WeekReset},
		{"1-month quota", tp.Per1Month, tp.Per1MonthReset},
	}
	var out []providers.Section
	for _, w := range windows {
		if w.pct == nil {
			continue
		}
		out = append(out, providers.Section{Name: w.name, UsedPct: pctOrMinus1(w.pct), Remaining: -1, ResetAt: epochMs(w.reset)})
	}
	return out
}

// tokenPlan 对应 Token Plan 用量字段。API 返回的百分比是 [0,1] 比值，重置时间是毫秒 epoch。
type tokenPlan struct {
	Per5Hour       *float64 `json:"per5HourPercentage"`
	Per5HourReset  *int64   `json:"per5HourResetTime"`
	Per1Week       *float64 `json:"per1WeekPercentage"`
	Per1WeekReset  *int64   `json:"per1WeekResetTime"`
	Per1Month      *float64 `json:"per1MonthPercentage"`
	Per1MonthReset *int64   `json:"per1MonthResetTime"`
}

// parseTokenPlan 兼容两种形态：直接对象，或 {"data": {...}} 信封。
// 字段缺失（无限额）时指针为 nil，由上层转成 UsedPct=-1。
func parseTokenPlan(raw []byte) (tokenPlan, error) {
	var tp tokenPlan
	if err := json.Unmarshal(raw, &tp); err != nil {
		return tp, err
	}
	if tp.Per5Hour == nil && tp.Per1Week == nil && tp.Per1Month == nil {
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
