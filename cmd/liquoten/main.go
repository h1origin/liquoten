// liquoten daemon：定时轮询各 provider 的额度，写缓存、记历史、发告警。
// 由 Liquoten.app 或 launchd 常驻运行；菜单栏只读它写出的 state.json。
//
// `liquoten statusline` 子命令作为 Claude Code 的 statusLine 命令运行，
// 把订阅用量落盘供 claudecode provider 读取。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lk/liquoten-cred/internal/alert"
	"github.com/lk/liquoten-cred/internal/config"
	"github.com/lk/liquoten-cred/internal/providers"
	_ "github.com/lk/liquoten-cred/internal/providers/bailian" // 注册 provider
	"github.com/lk/liquoten-cred/internal/providers/claudecode"
	_ "github.com/lk/liquoten-cred/internal/providers/codex"
	"github.com/lk/liquoten-cred/internal/store"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "statusline" {
		statusline()
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "providers" || os.Args[1] == "enable" || os.Args[1] == "disable") {
		if err := manageProviders(os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "login" {
		if err := login(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	configPath := flag.String("config", config.DefaultPath(), "配置文件路径（默认 ~/.config/liquoten/config.toml）")
	once := flag.Bool("once", false, "只轮询一次后退出（调试用）")
	cacheDir := flag.String("cache-dir", store.DefaultDir(), "缓存目录（默认 ~/.cache/liquoten）")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败 %s: %v", *configPath, err)
	}

	st := store.New(*cacheDir)

	// 通知通道：默认 "app" 把告警事件写进 notifications.jsonl，由 Liquoten.app 投递系统通知；
	// 设 notify = "osascript" 则退回 daemon 自己发 macOS 通知。
	var notifier alert.Notifier
	switch cfg.Notify {
	case "osascript":
		notifier = alert.OSAScriptNotifier
	default:
		notifier, err = alert.FileNotifier(*cacheDir)
		if err != nil {
			log.Fatalf("初始化通知通道失败: %v", err)
		}
	}

	engine, err := alert.New(cfg.Thresholds, *cacheDir, notifier)
	if err != nil {
		log.Fatalf("初始化告警引擎失败: %v", err)
	}

	provs := buildProviders(cfg, *cacheDir)
	if len(provs) == 0 {
		log.Printf("没有启用的 provider，可在 Liquoten 面板的“服务”页或配置文件 %s 中选择", *configPath)
		if err := st.WriteState([]providers.Balance{}); err != nil {
			log.Printf("写缓存失败: %v", err)
		}
		if *once {
			return
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	names := make([]string, 0, len(provs))
	for _, p := range provs {
		names = append(names, p.Name())
	}
	log.Printf("liquoten 启动：providers=[%s] 轮询间隔=%s 缓存目录=%s", strings.Join(names, ","), interval(cfg), *cacheDir)

	base := interval(cfg)
	every := func(p providers.Provider) time.Duration {
		if sp, ok := p.(providers.Scheduled); ok && sp.PollInterval() > 0 && sp.PollInterval() < base {
			return sp.PollInterval()
		}
		return base
	}
	latest := map[string]providers.Balance{}
	due := map[string]time.Time{}
	var lastPrune time.Time
	prune := func(now time.Time) {
		if now.Sub(lastPrune) < 24*time.Hour {
			return
		}
		lastPrune = now
		n, err := st.PruneHistory(now.Add(-store.HistoryRetention))
		if err != nil {
			log.Printf("清理历史失败: %v", err)
			return
		}
		if n > 0 {
			log.Printf("清理历史：删除 %d 条 %s 以前的记录", n, now.Add(-store.HistoryRetention).Format("2006-01-02 15:04"))
		}
	}

	poll := func(now time.Time) {
		prune(now)
		var polled []providers.Balance
		for _, p := range provs {
			if now.Before(due[p.Name()]) {
				continue
			}
			timeout := providerTimeout(cfg, p.Name())
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			bal, err := p.Query(ctx)
			cancel()
			if err != nil {
				bal = providers.Balance{Provider: p.Name(), FetchedAt: time.Now(), Err: err.Error()}
			}
			prev, seen := latest[p.Name()]
			if !seen || prev.Err != bal.Err || !reflect.DeepEqual(prev.Sections, bal.Sections) {
				logBalance(bal)
			}
			latest[p.Name()] = bal
			polled = append(polled, bal)
			d := every(p)
			due[p.Name()] = now.Add(d + time.Duration(rand.Float64()*0.2*float64(d)-0.1*float64(d)))
		}
		if len(polled) == 0 {
			return
		}
		all := make([]providers.Balance, 0, len(provs))
		for _, p := range provs {
			if b, ok := latest[p.Name()]; ok {
				all = append(all, b)
			}
		}
		if err := st.WriteState(all); err != nil {
			log.Printf("写缓存失败: %v", err)
		}
		if err := st.AppendHistory(polled); err != nil {
			log.Printf("写历史失败: %v", err)
		}
		if err := engine.Check(polled); err != nil {
			log.Printf("告警失败: %v", err)
		}
	}

	poll(time.Now())
	if *once {
		return
	}

	for {
		next := time.Time{}
		for _, t := range due {
			if next.IsZero() || t.Before(next) {
				next = t
			}
		}
		time.Sleep(time.Until(next))
		poll(time.Now())
	}
}

// statusline 读 Claude Code 传入的 stdin JSON，落盘用量并打印状态栏文本。
// 落盘失败不影响状态栏显示。
func statusline() {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return
	}
	in, err := claudecode.Capture(raw, store.DefaultDir(), time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "liquoten statusline: %v\n", err)
	}
	fmt.Println(claudecode.StatusLine(in))
}

func manageProviders(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	configPath := fs.String("config", config.DefaultPath(), "配置文件路径")
	fs.Parse(args)

	if cmd == "providers" {
		cfg, err := config.Load(*configPath)
		if err != nil {
			return fmt.Errorf("加载配置失败 %s: %w", *configPath, err)
		}
		type item struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		}
		var out []item
		for _, name := range providers.Names() {
			pc, ok := cfg.Providers[name]
			out = append(out, item{Name: name, Enabled: ok && pc.IsEnabled()})
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}

	name := fs.Arg(0)
	if !slices.Contains(providers.Names(), name) {
		return fmt.Errorf("用法: liquoten %s [-config 路径] <provider>（可选: %s）", cmd, strings.Join(providers.Names(), ", "))
	}
	return config.SetEnabled(*configPath, name, cmd == "enable")
}

func login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	configPath := fs.String("config", config.DefaultPath(), "配置文件路径")
	site := fs.String("site", "domestic", "站点：domestic 或 international")
	fs.Parse(args)
	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("用法: liquoten login [-config 路径] [-site domestic|international] <provider>")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("加载配置失败 %s: %w", *configPath, err)
	}
	pc := cfg.Providers[name]
	p, err := providers.New(name, providers.Options{APIKey: pc.APIKey, Command: pc.Command})
	if err != nil {
		return err
	}
	a, ok := p.(providers.Authenticator)
	if !ok {
		return fmt.Errorf("provider %q 不支持登录", name)
	}
	return a.Login(context.Background(), *site)
}

func buildProviders(cfg config.Config, dataDir string) []providers.Provider {
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []providers.Provider
	for _, name := range names {
		pc := cfg.Providers[name]
		if !pc.IsEnabled() {
			continue
		}
		p, err := providers.New(name, providers.Options{
			APIKey:  pc.APIKey,
			Command: pc.Command,
			Timeout: pc.Timeout,
			DataDir: dataDir,
		})
		if err != nil {
			log.Printf("跳过 provider %q: %v", name, err)
			continue
		}
		out = append(out, p)
	}
	return out
}

func interval(cfg config.Config) time.Duration {
	if cfg.PollInterval > 0 {
		return cfg.PollInterval
	}
	return config.DefaultPollInterval
}

func providerTimeout(cfg config.Config, name string) time.Duration {
	if pc, ok := cfg.Providers[name]; ok && pc.Timeout > 0 {
		return pc.Timeout
	}
	return config.DefaultTimeout
}

func logBalance(bal providers.Balance) {
	if bal.Err != "" {
		log.Printf("%s: 查询失败: %s", bal.Provider, bal.Err)
		return
	}
	parts := make([]string, 0, len(bal.Sections))
	for _, s := range bal.Sections {
		if s.UsedPct < 0 && s.Remaining >= 0 {
			parts = append(parts, fmt.Sprintf("%s=%g", s.Name, s.Remaining))
			continue
		}
		if s.UsedPct < 0 {
			parts = append(parts, fmt.Sprintf("%s=无限制", s.Name))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%.1f%%", s.Name, s.UsedPct))
	}
	log.Printf("%s: %s", bal.Provider, strings.Join(parts, " "))
}
