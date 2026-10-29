// Package qoder 查询 Qoder（国际版）与 Qoder CN（国内版）的 Credits 额度。
//
// 用户在 Qoder 账号的 Integrations 页生成个人访问令牌（PAT），先换取短期
// job token，再请求 /api/v2/quota/usage。两个版本只有 OpenAPI 域名不同。
// 该接口未见官方文档，流程参照社区插件 pi-provider-qoder-cn；官方 Agent SDK
// 的 getUsageInfo() 返回同名字段（userQuota / addOnQuota / isQuotaExceeded）。
package qoder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

// Site 描述一个 Qoder 版本。
type Site struct {
	Name       string
	OpenAPIURL string
}

var (
	Global = Site{Name: "qoder", OpenAPIURL: "https://openapi.qoder.sh"}
	CN     = Site{Name: "qodercn", OpenAPIURL: "https://openapi.qoder.com.cn"}
)

const defaultTimeout = 20 * time.Second

// Provider 用 PAT 查询 Credits 额度。
type Provider struct {
	site    Site
	pat     string
	timeout time.Duration
	client  *http.Client
	now     func() time.Time

	mu       sync.Mutex
	jobToken string
	expires  time.Time
}

func init() {
	for _, s := range []Site{Global, CN} {
		providers.Register(s.Name, func(opts providers.Options) (providers.Provider, error) {
			return New(s, opts), nil
		})
	}
}

// New 构造 provider。
func New(site Site, opts providers.Options) *Provider {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Provider{
		site:    site,
		pat:     strings.TrimSpace(opts.APIKey),
		timeout: timeout,
		client:  &http.Client{},
		now:     time.Now,
	}
}

func (p *Provider) Name() string { return p.site.Name }

func (p *Provider) RequiresAPIKey() bool { return true }

// PollInterval 额度按计费周期累计，30 分钟查一次。
func (p *Provider) PollInterval() time.Duration { return 30 * time.Minute }

// Query 查询本计费周期 Credits 用量与加购余额。
func (p *Provider) Query(ctx context.Context) (providers.Balance, error) {
	bal := providers.Balance{Provider: p.site.Name, FetchedAt: p.now()}
	if p.pat == "" {
		return bal, errors.New("未设置 API Key")
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	token, err := p.token(ctx)
	if err != nil {
		return bal, err
	}
	raw, status, err := p.do(ctx, http.MethodGet, "/api/v2/quota/usage", token, nil)
	if err == nil && status == http.StatusUnauthorized {
		p.mu.Lock()
		p.jobToken = ""
		p.mu.Unlock()
		if token, err = p.token(ctx); err != nil {
			return bal, err
		}
		raw, status, err = p.do(ctx, http.MethodGet, "/api/v2/quota/usage", token, nil)
	}
	if err != nil {
		return bal, fmt.Errorf("查询额度失败: %w", err)
	}
	if status != http.StatusOK {
		return bal, fmt.Errorf("查询额度失败: HTTP %d %s", status, snippet(raw))
	}
	sections, err := parseUsage(raw)
	if err != nil {
		return bal, fmt.Errorf("解析额度失败: %w", err)
	}
	bal.Sections = sections
	return bal, nil
}

// token 返回缓存的 job token，过期前 5 分钟用 PAT 重新换取。
func (p *Provider) token(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.jobToken != "" && p.now().Before(p.expires) {
		return p.jobToken, nil
	}
	body, _ := json.Marshal(map[string]string{"personal_token": p.pat})
	raw, status, err := p.do(ctx, http.MethodPost, "/api/v1/jobToken/exchange", "", body)
	if err != nil {
		return "", fmt.Errorf("验证 API Key 失败: %w", err)
	}
	if status != http.StatusOK {
		if status == http.StatusBadRequest || status == http.StatusUnauthorized || status == http.StatusForbidden {
			return "", fmt.Errorf("API Key 无效或已过期（HTTP %d）", status)
		}
		return "", fmt.Errorf("验证 API Key 失败: HTTP %d %s", status, snippet(raw))
	}
	var ex struct {
		Token     string          `json:"token"`
		ExpiresAt json.RawMessage `json:"expires_at"`
		ExpiresIn float64         `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &ex); err != nil || ex.Token == "" {
		return "", fmt.Errorf("验证 API Key 失败：响应中没有 token（%s）", snippet(raw))
	}
	exp := expiry(ex.ExpiresAt, ex.ExpiresIn, p.now())
	p.jobToken, p.expires = ex.Token, exp.Add(-5*time.Minute)
	return p.jobToken, nil
}

func (p *Provider) do(ctx context.Context, method, path, bearer string, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.site.OpenAPIURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "liquoten")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cosy-Version", "1.0.1")
		req.Header.Set("Cosy-ClientType", "5")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return raw, resp.StatusCode, err
}

type bucket struct {
	Total      *float64 `json:"total"`
	Cap        *float64 `json:"cap"`
	Used       *float64 `json:"used"`
	Remaining  *float64 `json:"remaining"`
	Percentage *float64 `json:"percentage"`
	Available  *bool    `json:"available"`
}

func (b *bucket) has() bool {
	if b == nil || (b.Available != nil && !*b.Available) {
		return false
	}
	return num(b.Total) > 0 || num(b.Cap) > 0 || num(b.Used) > 0 || num(b.Remaining) > 0
}

func (b *bucket) total() float64 {
	if b.Total != nil {
		return *b.Total
	}
	return num(b.Cap)
}

// usedPct 优先用 used/total 计算；否则用 percentage（[0,1] 比值或百分数）。
func (b *bucket) usedPct() float64 {
	if t := b.total(); t > 0 && b.Used != nil {
		return *b.Used / t * 100
	}
	if b.Percentage != nil {
		if *b.Percentage <= 1 {
			return *b.Percentage * 100
		}
		return *b.Percentage
	}
	return -1
}

func (b *bucket) remaining() float64 {
	if b.Remaining != nil {
		return *b.Remaining
	}
	if b.Used != nil && b.total() > 0 {
		return b.total() - *b.Used
	}
	return -1
}

// parseUsage 把本计费周期额度归一为 "1-month quota"，加购余额归为 "credits"。
// 个人版用 userQuota；团队版成员的 userQuota 全为 0，额度在共享的 orgResourcePackage 里。
// 字段同时兼容 camelCase 与 snake_case，以及 {"data": {...}} 信封。
func parseUsage(raw []byte) ([]providers.Section, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	_, hasCamel := m["userQuota"]
	_, hasSnake := m["user_quota"]
	if d, ok := m["data"]; ok && !hasCamel && !hasSnake {
		var inner map[string]json.RawMessage
		if json.Unmarshal(d, &inner) == nil && inner != nil {
			m = inner
		}
	}
	pick := func(camel, snake string) *bucket {
		v, ok := m[camel]
		if !ok {
			v, ok = m[snake]
		}
		if !ok {
			return nil
		}
		var b bucket
		if json.Unmarshal(v, &b) != nil {
			return nil
		}
		return &b
	}

	var resetAt time.Time
	for _, k := range []string{"expiresAt", "expires_at"} {
		if v, ok := m[k]; ok {
			resetAt = expiry(v, 0, time.Time{})
			break
		}
	}

	var out []providers.Section
	quota := pick("userQuota", "user_quota")
	if !quota.has() {
		quota = pick("orgResourcePackage", "org_resource_package")
	}
	if quota.has() {
		out = append(out, providers.Section{Name: "1-month quota", UsedPct: quota.usedPct(), Remaining: quota.remaining(), ResetAt: resetAt})
	}
	if b := pick("addOnQuota", "add_on_quota"); b.has() {
		out = append(out, providers.Section{Name: "credits", UsedPct: -1, Remaining: b.remaining()})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("响应中没有额度数据（%s）", snippet(raw))
	}
	return out, nil
}

// expiry 解析毫秒/秒 epoch 或 RFC3339；year ≥ 9999 视为无期限。都没有时按 expiresIn 秒推算。
func expiry(v json.RawMessage, expiresIn float64, now time.Time) time.Time {
	var t time.Time
	var n float64
	var s string
	switch {
	case json.Unmarshal(v, &n) == nil && n > 0:
		if n < 1e12 {
			n *= 1000
		}
		t = time.UnixMilli(int64(n))
	case json.Unmarshal(v, &s) == nil && s != "":
		t, _ = time.Parse(time.RFC3339, s)
	}
	if !t.IsZero() && t.Year() >= 9999 {
		return time.Time{}
	}
	if t.IsZero() && expiresIn > 0 {
		t = now.Add(time.Duration(expiresIn) * time.Second)
	}
	if t.IsZero() && !now.IsZero() {
		t = now.Add(time.Hour)
	}
	return t
}

func num(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
