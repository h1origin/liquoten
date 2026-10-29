// Package bailian 实现阿里云百炼(Token Plan)额度查询。
//
// 支持两种模式：配置了阿里云 AK/SK（或 ~/.bailian/config.json 里有）时，
// 直接签名调用 OpenAPI 换取 CLI access token，再查控制台网关；否则执行
// 官方开源 CLI(bailian-cli)的 `bl usage token-plan --output json`。
//
// Token Plan 的余量语义是滚动配额百分比：5 小时窗口 + 1 周窗口的已用百分比
// 及重置时间，与官方控制台展示一致，没有"绝对剩余 credits"接口。
package bailian

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

// Name 是注册表里的 provider 名称。
const Name = "bailian"

const defaultTimeout = 45 * time.Second

const loginTimeout = 5 * time.Minute

// Provider 查询百炼 Token Plan 用量，有 AK/SK 时直连，否则执行 bl。
type Provider struct {
	command         string
	apiKey          string
	accessKeyID     string
	accessKeySecret string
	region          string
	site            string
	switchAgent     json.RawMessage
	timeout         time.Duration
	tokenBaseURL    string
	gatewayBaseURL  string
	tokenMu         sync.Mutex
	token           string
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
	p := &Provider{
		command:         cmd,
		apiKey:          opts.APIKey,
		accessKeyID:     opts.AccessKeyID,
		accessKeySecret: opts.AccessKeySecret,
		region:          defaultRegion,
		site:            defaultSite,
		timeout:         timeout,
	}
	p.loadBLConfig()
	return p
}

// Name 返回注册表名称。
func (p *Provider) Name() string { return Name }

// Query 查询 Token Plan 的 5 小时 / 1 周窗口用量。
func (p *Provider) Query(ctx context.Context) (providers.Balance, error) {
	bal := providers.Balance{Provider: Name, FetchedAt: time.Now()}

	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	if p.hasCredentials() {
		return p.queryDirect(ctx, bal)
	}

	out, err := p.run(ctx, "usage", "token-plan", "--output", "json")
	if err != nil {
		return bal, fmt.Errorf("查询 Token Plan 用量失败: %w", err)
	}
	tp, err := parseTokenPlan(out)
	if err != nil {
		return bal, fmt.Errorf("解析 token-plan 输出失败: %w", err)
	}

	bal.Sections = sections(tp)
	if len(bal.Sections) == 0 {
		return bal, fmt.Errorf("token-plan 输出中没有可识别的额度字段: %s", strings.TrimSpace(string(out)))
	}
	return bal, nil
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

func (p *Provider) Login(ctx context.Context, site string) error {
	if site != "domestic" && site != "international" {
		return fmt.Errorf("未知站点 %q（可选: domestic, international）", site)
	}
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	if _, err := p.run(ctx, "auth", "login", "--console", "--console-site", site); err != nil {
		return fmt.Errorf("百炼控制台登录失败: %w", err)
	}
	return nil
}

// npm 全局目录常见位置；从 Finder 启动的 App 的 PATH 里没有这些目录。
var searchDirs = func() []string {
	home, _ := os.UserHomeDir()
	return []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".volta", "bin"),
		filepath.Join(home, ".local", "bin"),
	}
}

// lookup 按 PATH 与常见安装目录查找可执行文件，返回绝对路径。
func lookup(name string) (string, bool) {
	if filepath.IsAbs(name) {
		if st, err := os.Stat(name); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return name, true
		}
		return "", false
	}
	if path, err := exec.LookPath(name); err == nil {
		if abs, err := filepath.Abs(path); err == nil {
			return abs, true
		}
	}
	for _, dir := range searchDirs() {
		if path, ok := lookup(filepath.Join(dir, name)); ok {
			return path, true
		}
	}
	return "", false
}

// Preflight 检查 bl 是否已安装，返回其绝对路径；直连模式不依赖 bl。
func (p *Provider) Preflight(ctx context.Context) (string, error) {
	if p.hasCredentials() {
		return p.command, nil
	}
	path, ok := lookup(p.command)
	if !ok && p.command != "bl" {
		path, ok = lookup("bl")
	}
	if !ok {
		return "", fmt.Errorf("没有找到百炼命令行工具 bl：%w", providers.ErrNotInstalled)
	}
	return path, nil
}

// Install 用 npm 全局安装 bailian-cli（需要 Node.js ≥ 22.12）。
func (p *Provider) Install(ctx context.Context) error {
	npm, ok := lookup("npm")
	if !ok {
		return errors.New("没有找到 npm。请先安装 Node.js 22.12 或更高版本（https://nodejs.org，或在终端执行 brew install node），再重试")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, npm, "install", "-g", "bailian-cli")
	cmd.Env = withDirInPath(os.Environ(), filepath.Dir(npm))
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 600 {
			msg = "…" + msg[len(msg)-600:]
		}
		return fmt.Errorf("npm install -g bailian-cli 失败: %v\n%s", err, msg)
	}
	if _, ok := lookup("bl"); !ok {
		return errors.New("bailian-cli 已安装，但仍找不到 bl 命令，请在终端执行 npm prefix -g 检查全局安装目录")
	}
	return nil
}

// withDirInPath 把 dir 放到 PATH 最前：bl 与 npm 都是 `#!/usr/bin/env node` 脚本，
// 需要在 PATH 中找到与之同目录的 node。
func withDirInPath(env []string, dir string) []string {
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			env[i] = "PATH=" + dir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
			return env
		}
	}
	return append(env, "PATH="+dir)
}

// run 执行 CLI 并返回 stdout；失败时把 stderr 带进错误信息。
func (p *Provider) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, p.command, args...)
	cmd.Env = os.Environ()
	if path, ok := lookup(p.command); ok {
		cmd.Path = path
		cmd.Env = withDirInPath(cmd.Env, filepath.Dir(path))
	}
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
