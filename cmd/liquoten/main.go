// liquoten daemon：定时轮询各 provider 的额度，写缓存、记历史、发告警。
// 由 liquoten.app 或 launchd 常驻运行；菜单栏只读它写出的 state.json。
//
// `liquoten statusline` 子命令作为 Claude Code 的 statusLine 命令运行，
// 把订阅用量落盘供 claudecode provider 读取。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"reflect"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/h1origin/liquoten/internal/alert"
	"github.com/h1origin/liquoten/internal/config"
	"github.com/h1origin/liquoten/internal/providers"
	_ "github.com/h1origin/liquoten/internal/providers/bailian" // 注册 provider
	"github.com/h1origin/liquoten/internal/providers/claudecode"
	_ "github.com/h1origin/liquoten/internal/providers/codex"
	_ "github.com/h1origin/liquoten/internal/providers/qoder"
	"github.com/h1origin/liquoten/internal/store"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "statusline" {
		statusline()
		return
	}
	if len(os.Args) > 1 && slices.Contains([]string{"providers", "enable", "disable", "set-access-key", "clear-access-key"}, os.Args[1]) {
		if err := manageProviders(os.Args[1], os.Args[2:]); err != nil {
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

	// 通知通道：默认 "app" 把告警事件写进 notifications.jsonl，由 liquoten.app 投递系统通知；
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

	engine, err := alert.New(cfg.Thresholds, cfg.AlertRemaining, *cacheDir, notifier)
	if err != nil {
		log.Fatalf("初始化告警引擎失败: %v", err)
	}

	provs := buildProviders(cfg, *cacheDir)
	if len(provs) == 0 {
		log.Printf("没有启用的 provider，可在 liquoten 面板的“服务”页或配置文件 %s 中选择", *configPath)
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
		return everyInterval(p, base)
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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)
	defer signal.Stop(sigCh)
	var lastSignalPoll time.Time

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
		timer := time.NewTimer(time.Until(next))
		select {
		case <-timer.C:
			poll(time.Now())
		case <-sigCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			now := time.Now()
			if !refreshSignalAllowed(lastSignalPoll, now) {
				log.Printf("忽略 10 秒内的重复刷新信号")
				continue
			}
			lastSignalPoll = now
			log.Printf("收到 SIGUSR1，立即刷新")
			handleRefreshSignal(provs, due, now)
			poll(now)
		}
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

// manageProviders 实现 providers / enable / disable / set-access-key / clear-access-key 子命令，供 App 的“服务”页调用。
//
//	enable [-api-key-stdin] <name>：需要 key 的先验证 key 再写入配置
//	disable <name>：停用，并删除配置中的 api_key / access_key_id / access_key_secret
//	set-access-key <name>：从标准输入读 AK ID 和 secret，验证通过后写入配置
//	clear-access-key <name>：删除配置中的 access_key_id / access_key_secret，不改 enabled
func manageProviders(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	configPath := fs.String("config", config.DefaultPath(), "配置文件路径")
	keyStdin := fs.Bool("api-key-stdin", false, "从标准输入读取 API Key（仅 enable）")
	fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("加载配置失败 %s: %w", *configPath, err)
	}

	if cmd == "providers" {
		type item struct {
			Name            string `json:"name"`
			Enabled         bool   `json:"enabled"`
			RequiresAPIKey  bool   `json:"requires_api_key"`
			AccessKeySource string `json:"access_key_source"`
			AccessKeyMasked string `json:"access_key_masked"`
		}
		var out []item
		for _, name := range providers.Names() {
			pc, ok := cfg.Providers[name]
			it := item{Name: name, Enabled: ok && pc.IsEnabled()}
			if p, err := providers.New(name, providers.Options{
				APIKey:          pc.APIKey,
				AccessKeyID:     pc.AccessKeyID,
				AccessKeySecret: pc.AccessKeySecret,
				Timeout:         pc.Timeout,
				DataDir:         store.DefaultDir(),
			}); err == nil {
				k, ok := p.(providers.KeyRequired)
				it.RequiresAPIKey = ok && k.RequiresAPIKey()
				if r, ok := p.(providers.AccessKeyReporter); ok {
					it.AccessKeySource, it.AccessKeyMasked = r.AccessKeyStatus()
				}
			}
			out = append(out, it)
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}

	name := fs.Arg(0)
	if !slices.Contains(providers.Names(), name) {
		return fmt.Errorf("用法: liquoten %s [-config 路径] <provider>（可选: %s）", cmd, strings.Join(providers.Names(), ", "))
	}
	pc := cfg.Providers[name]
	opts := providers.Options{APIKey: pc.APIKey, AccessKeyID: pc.AccessKeyID, AccessKeySecret: pc.AccessKeySecret, Timeout: pc.Timeout, DataDir: store.DefaultDir()}
	ctx := context.Background()

	switch cmd {
	case "disable":
		off := false
		return config.SetProvider(*configPath, name, config.ProviderUpdate{
			Enabled: &off,
			Delete:  []string{"api_key", "access_key_id", "access_key_secret"},
		})

	case "clear-access-key":
		p, err := providers.New(name, opts)
		if err != nil {
			return err
		}
		if _, ok := p.(providers.AccessKeyReporter); !ok {
			return fmt.Errorf("provider %q 不支持 AccessKey", name)
		}
		return config.SetProvider(*configPath, name, config.ProviderUpdate{
			Delete: []string{"access_key_id", "access_key_secret"},
		})

	case "set-access-key":
		p, err := providers.New(name, opts)
		if err != nil {
			return err
		}
		if _, ok := p.(providers.AccessKeyReporter); !ok {
			return fmt.Errorf("provider %q 不支持 AccessKey", name)
		}
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
		if err != nil {
			return err
		}
		parts := strings.SplitN(string(raw), "\n", 3)
		id := strings.TrimSpace(parts[0])
		secret := ""
		if len(parts) > 1 {
			secret = strings.TrimSpace(parts[1])
		}
		if id == "" || secret == "" {
			return errors.New("请填写 AccessKey ID 和 AccessKey Secret")
		}
		opts.AccessKeyID = id
		opts.AccessKeySecret = secret
		p, err = providers.New(name, opts)
		if err != nil {
			return err
		}
		qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = p.Query(qctx)
		cancel()
		if err != nil {
			return err
		}
		on := true
		return config.SetProvider(*configPath, name, config.ProviderUpdate{
			Enabled:         &on,
			AccessKeyID:     &id,
			AccessKeySecret: &secret,
		})
	}

	on := true
	update := config.ProviderUpdate{Enabled: &on}
	if *keyStdin {
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
		if err != nil {
			return err
		}
		key := strings.TrimSpace(string(raw))
		opts.APIKey = key
		update.APIKey = &key
	}
	p, err := providers.New(name, opts)
	if err != nil {
		return err
	}
	if k, ok := p.(providers.KeyRequired); ok && k.RequiresAPIKey() {
		if opts.APIKey == "" {
			return errors.New("请填写 API Key")
		}
		qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := p.Query(qctx)
		cancel()
		if err != nil {
			return err
		}
	}
	return config.SetProvider(*configPath, name, update)
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
			APIKey:          pc.APIKey,
			AccessKeyID:     pc.AccessKeyID,
			AccessKeySecret: pc.AccessKeySecret,
			Timeout:         pc.Timeout,
			DataDir:         dataDir,
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

func everyInterval(p providers.Provider, base time.Duration) time.Duration {
	if sp, ok := p.(providers.Scheduled); ok && sp.PollInterval() > 0 {
		return sp.PollInterval()
	}
	return base
}

const refreshSignalDebounce = 10 * time.Second

func refreshSignalAllowed(last, now time.Time) bool {
	return last.IsZero() || now.Sub(last) >= refreshSignalDebounce
}

func handleRefreshSignal(provs []providers.Provider, due map[string]time.Time, now time.Time) {
	for _, p := range provs {
		if r, ok := p.(providers.Refresher); ok {
			r.RefreshNow()
		}
		due[p.Name()] = now
	}
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
