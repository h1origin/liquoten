// liquoten daemon：定时轮询各 provider 的额度，写缓存、记历史、发告警。
// 由 Liquoten.app 或 launchd 常驻运行；菜单栏只读它写出的 state.json。
//
// `liquoten statusline` 子命令作为 Claude Code 的 statusLine 命令运行，
// 把订阅用量落盘供 claudecode provider 读取。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/lk/liquoten-cred/internal/alert"
	"github.com/lk/liquoten-cred/internal/config"
	"github.com/lk/liquoten-cred/internal/providers"
	_ "github.com/lk/liquoten-cred/internal/providers/bailian" // 注册 provider
	"github.com/lk/liquoten-cred/internal/providers/claudecode"
	"github.com/lk/liquoten-cred/internal/store"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "statusline" {
		statusline()
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
		log.Fatal("没有启用的 provider，请检查配置文件")
	}
	names := make([]string, 0, len(provs))
	for _, p := range provs {
		names = append(names, p.Name())
	}
	log.Printf("liquoten 启动：providers=[%s] 轮询间隔=%s 缓存目录=%s", strings.Join(names, ","), interval(cfg), *cacheDir)

	poll := func() {
		var balances []providers.Balance
		for _, p := range provs {
			timeout := providerTimeout(cfg, p.Name())
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			bal, err := p.Query(ctx)
			cancel()
			if err != nil {
				bal = providers.Balance{Provider: p.Name(), FetchedAt: time.Now(), Err: err.Error()}
			}
			balances = append(balances, bal)
			logBalance(bal)
		}
		if err := st.WriteState(balances); err != nil {
			log.Printf("写缓存失败: %v", err)
		}
		if err := st.AppendHistory(balances); err != nil {
			log.Printf("写历史失败: %v", err)
		}
		if err := engine.Check(balances); err != nil {
			log.Printf("告警失败: %v", err)
		}
	}

	poll()
	if *once {
		return
	}

	// 每次轮询间隔加 ±10% 抖动，避免固定节拍
	base := interval(cfg)
	for {
		jitter := base + time.Duration(rand.Float64()*0.2*float64(base)-0.1*float64(base))
		time.Sleep(jitter)
		poll()
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
		if s.UsedPct < 0 {
			parts = append(parts, fmt.Sprintf("%s=无限制", s.Name))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%.1f%%", s.Name, s.UsedPct))
	}
	log.Printf("%s: %s", bal.Provider, strings.Join(parts, " "))
}
