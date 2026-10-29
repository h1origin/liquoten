package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

const Name = "codex"

const maxFilesScanned = 5

const defaultTimeout = 20 * time.Second

const defaultBaseURL = "https://chatgpt.com/backend-api"

const usagePath = "/wham/usage"

const resetCreditsPath = "/wham/rate-limit-reset-credits"

const usageInterval = time.Hour

const resetCreditsInterval = 12 * time.Hour

var errNoAuth = errors.New("没有 Codex 登录凭据")

type Provider struct {
	home               string
	now                func() time.Time
	cache              map[string]cached
	timeout            time.Duration
	baseURL            string
	lastUsageAttempt   time.Time
	liveUsage          *rateLimits
	liveUsageAt        time.Time
	lastResetAttempt   time.Time
	resetCredits       []resetCredit
	resetCreditsLoaded bool
}

type cached struct {
	mod  time.Time
	size int64
	ev   *event
}

type authFile struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

type authCredentials struct {
	accessToken string
	accountID   string
}

type liveUsageResponse struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		Primary   *liveWindow `json:"primary_window"`
		Secondary *liveWindow `json:"secondary_window"`
	} `json:"rate_limit"`
	Credits      *credits `json:"credits"`
	ResetCredits struct {
		AvailableCount *int `json:"available_count"`
	} `json:"rate_limit_reset_credits"`
}

type liveWindow struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds int64    `json:"limit_window_seconds"`
	ResetAt            int64    `json:"reset_at"`
}

type resetCreditsResponse struct {
	Credits []resetCredit `json:"credits"`
}

type resetCredit struct {
	Status            string    `json:"status"`
	IsSupportedByPlan bool      `json:"is_supported_by_plan"`
	ExpiresAt         time.Time `json:"expires_at"`
}

func init() {
	providers.Register(Name, func(opts providers.Options) (providers.Provider, error) {
		return newProvider(DefaultHome(), opts.Timeout), nil
	})
}

func DefaultHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func New(home string) *Provider {
	return newProvider(home, 0)
}

func newProvider(home string, timeout time.Duration) *Provider {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Provider{
		home:    home,
		now:     time.Now,
		cache:   map[string]cached{},
		timeout: timeout,
		baseURL: defaultBaseURL,
	}
}

func (p *Provider) Name() string { return Name }

func (p *Provider) RefreshNow() {
	p.lastUsageAttempt = time.Time{}
	p.lastResetAttempt = time.Time{}
}

func (p *Provider) Query(ctx context.Context) (providers.Balance, error) {
	now := p.now()
	bal := providers.Balance{Provider: Name, FetchedAt: now}

	localEv, localErr := p.latestLocal(ctx)
	if ctx.Err() != nil {
		return bal, ctx.Err()
	}
	p.refreshLiveUsage(ctx, now)
	p.refreshResetCredits(ctx, now)

	var rl *rateLimits
	var ts time.Time
	switch {
	case localEv != nil && (p.liveUsage == nil || !p.liveUsageAt.After(localEv.Timestamp)):
		rl, ts = localEv.Payload.RateLimits, localEv.Timestamp
	case p.liveUsage != nil:
		rl, ts = p.liveUsage, p.liveUsageAt
	}
	if rl == nil {
		if localErr != nil {
			return bal, localErr
		}
		return bal, errors.New("尚无数据：需要先在 Codex 里对话一次（数据来自 ~/.codex/sessions）")
	}

	bal.FetchedAt = ts
	bal.Sections = sections(rl, ts, now)
	if len(bal.Sections) == 0 {
		return bal, errors.New("Codex 会话记录里没有用量窗口数据")
	}
	if p.resetCreditsLoaded {
		count, earliest := availableResetCredits(p.resetCredits, now)
		bal.Sections = append(bal.Sections, providers.Section{
			Name:      "reset-credits",
			UsedPct:   -1,
			Remaining: float64(count),
			ResetAt:   earliest,
		})
	}
	return bal, nil
}

func (p *Provider) latestLocal(ctx context.Context) (*event, error) {
	files, err := p.recentSessions()
	if err != nil {
		return nil, err
	}
	var ev *event
	for _, f := range files {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		ev, err = p.lastRateLimits(f)
		if err != nil {
			return nil, err
		}
		if ev != nil {
			return ev, nil
		}
	}
	return nil, errors.New("尚无数据：需要先在 Codex 里对话一次（数据来自 ~/.codex/sessions）")
}

func (p *Provider) readAuth() (authCredentials, bool) {
	data, err := os.ReadFile(filepath.Join(p.home, "auth.json"))
	if err != nil {
		return authCredentials{}, false
	}
	var a authFile
	if err := json.Unmarshal(data, &a); err != nil || a.Tokens.AccessToken == "" {
		return authCredentials{}, false
	}
	return authCredentials{accessToken: a.Tokens.AccessToken, accountID: a.Tokens.AccountID}, true
}

func (p *Provider) getJSON(ctx context.Context, path string, out any) error {
	auth, ok := p.readAuth()
	if !ok {
		return errNoAuth
	}
	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(p.baseURL, "/")+path, nil)
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+auth.accessToken)
	if auth.accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", auth.accountID)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "liquoten")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}
	return nil
}

func (p *Provider) refreshLiveUsage(ctx context.Context, now time.Time) {
	if !p.lastUsageAttempt.IsZero() && now.Sub(p.lastUsageAttempt) < usageInterval {
		return
	}
	var usage liveUsageResponse
	if err := p.getJSON(ctx, usagePath, &usage); err != nil {
		if errors.Is(err, errNoAuth) {
			return
		}
		p.lastUsageAttempt = now
		log.Printf("codex 实时额度查询失败: %v", err)
		return
	}
	p.lastUsageAttempt = now
	p.liveUsage = usage.rateLimits()
	p.liveUsageAt = now
	if usage.ResetCredits.AvailableCount != nil && p.resetCreditsLoaded {
		count, _ := availableResetCredits(p.resetCredits, now)
		if *usage.ResetCredits.AvailableCount != count {
			p.lastResetAttempt = time.Time{}
		}
	}
}

func (p *Provider) refreshResetCredits(ctx context.Context, now time.Time) {
	if !p.lastResetAttempt.IsZero() && now.Sub(p.lastResetAttempt) < resetCreditsInterval {
		return
	}
	var credits resetCreditsResponse
	if err := p.getJSON(ctx, resetCreditsPath, &credits); err != nil {
		if errors.Is(err, errNoAuth) {
			return
		}
		p.lastResetAttempt = now
		log.Printf("codex 重置卡查询失败: %v", err)
		return
	}
	p.lastResetAttempt = now
	p.resetCredits = credits.Credits
	p.resetCreditsLoaded = true
}

func (u liveUsageResponse) rateLimits() *rateLimits {
	rl := &rateLimits{PlanType: u.PlanType, Credits: u.Credits}
	rl.Primary = liveWindowToWindow(u.RateLimit.Primary)
	rl.Secondary = liveWindowToWindow(u.RateLimit.Secondary)
	return rl
}

func liveWindowToWindow(w *liveWindow) *window {
	if w == nil {
		return nil
	}
	return &window{
		UsedPercent:   w.UsedPercent,
		WindowMinutes: int(w.LimitWindowSeconds / 60),
		ResetsAt:      w.ResetAt,
	}
}

func availableResetCredits(credits []resetCredit, now time.Time) (int, time.Time) {
	count := 0
	var earliest time.Time
	for _, c := range credits {
		if c.Status != "available" || !c.IsSupportedByPlan {
			continue
		}
		if c.ExpiresAt.IsZero() || !c.ExpiresAt.After(now) {
			continue
		}
		count++
		if earliest.IsZero() || c.ExpiresAt.Before(earliest) {
			earliest = c.ExpiresAt
		}
	}
	return count, earliest
}

type fileInfo struct {
	path string
	mod  time.Time
	size int64
}

func (p *Provider) recentSessions() ([]fileInfo, error) {
	root := filepath.Join(p.home, "sessions")
	var files []fileInfo
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == root {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, fileInfo{path: path, mod: info.ModTime(), size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("读取 Codex 会话目录 %s 失败: %w", root, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("尚无数据：找不到 Codex 会话记录（%s），请先安装 Codex 并对话一次", root)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > maxFilesScanned {
		files = files[:maxFilesScanned]
	}
	return files, nil
}

func (p *Provider) lastRateLimits(f fileInfo) (*event, error) {
	if c, ok := p.cache[f.path]; ok && c.mod.Equal(f.mod) && c.size == f.size {
		return c.ev, nil
	}
	file, err := os.Open(f.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	ev, err := parseLastRateLimits(file)
	if err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", filepath.Base(f.path), err)
	}
	p.cache[f.path] = cached{mod: f.mod, size: f.size, ev: ev}
	return ev, nil
}

type event struct {
	Timestamp time.Time `json:"timestamp"`
	Payload   struct {
		Type       string      `json:"type"`
		RateLimits *rateLimits `json:"rate_limits"`
	} `json:"payload"`
}

type rateLimits struct {
	Primary   *window  `json:"primary"`
	Secondary *window  `json:"secondary"`
	Credits   *credits `json:"credits"`
	PlanType  string   `json:"plan_type"`
}

type window struct {
	UsedPercent     *float64 `json:"used_percent"`
	WindowMinutes   int      `json:"window_minutes"`
	ResetsAt        int64    `json:"resets_at"`
	ResetsInSeconds int64    `json:"resets_in_seconds"`
}

type credits struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance"`
}

var marker = []byte(`"rate_limits"`)

func parseLastRateLimits(r io.Reader) (*event, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var last *event
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && bytes.Contains(line, marker) {
			var ev event
			if jerr := json.Unmarshal(line, &ev); jerr == nil && ev.Payload.RateLimits != nil {
				last = &ev
			}
		}
		if err == io.EOF {
			return last, nil
		}
		if err != nil {
			return last, err
		}
	}
}

func sections(rl *rateLimits, ts, now time.Time) []providers.Section {
	var out []providers.Section
	for _, w := range []*window{rl.Primary, rl.Secondary} {
		if w == nil || w.UsedPercent == nil {
			continue
		}
		s := providers.Section{Name: windowName(w.WindowMinutes), UsedPct: *w.UsedPercent, Remaining: -1}
		var reset time.Time
		if w.ResetsAt > 0 {
			reset = time.Unix(w.ResetsAt, 0)
		} else if w.ResetsInSeconds > 0 && !ts.IsZero() {
			reset = ts.Add(time.Duration(w.ResetsInSeconds) * time.Second)
		}
		if !reset.IsZero() {
			if now.After(reset) {
				s.UsedPct = 0
			} else {
				s.ResetAt = reset
			}
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return windowOrder(out[i].Name) < windowOrder(out[j].Name) })
	if c := rl.Credits; c != nil && c.HasCredits && !c.Unlimited {
		if v, err := strconv.ParseFloat(c.Balance, 64); err == nil {
			out = append(out, providers.Section{Name: "credits", UsedPct: -1, Remaining: v})
		}
	}
	return out
}

func windowName(minutes int) string {
	switch minutes {
	case 300:
		return "5-hour quota"
	case 10080:
		return "1-week quota"
	}
	if minutes > 0 && minutes%1440 == 0 {
		return fmt.Sprintf("%d-day quota", minutes/1440)
	}
	if minutes > 0 && minutes%60 == 0 {
		return fmt.Sprintf("%d-hour quota", minutes/60)
	}
	return fmt.Sprintf("%d-minute quota", minutes)
}

func windowOrder(name string) int {
	switch name {
	case "5-hour quota":
		return 0
	case "1-week quota":
		return 1
	}
	return 2
}
