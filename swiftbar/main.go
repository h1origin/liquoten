// liquoten-bar：SwiftBar 菜单栏插件。只读 daemon 写出的 state.json，无网络调用。
//
// 构建与安装：
//
//	go build -o ~/.local/bin/liquoten-bar ./swiftbar
//	ln -s ~/.local/bin/liquoten-bar "$SWIFTBAR_PLUGIN_DIR/liquoten.15m.sh"
//
// SwiftBar 每次刷新会执行一次本程序，按 xbar 格式输出菜单。
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/h1origin/liquoten/internal/config"
	"github.com/h1origin/liquoten/internal/providers"
	"github.com/h1origin/liquoten/internal/store"
)

const (
	bailianConsoleURL = "https://bailian.console.aliyun.com/"
	claudeUsageURL    = "https://claude.ai/settings/usage"
)

func main() {
	cfg, _ := config.Load(config.DefaultPath())
	st := store.New(store.DefaultDir())

	balances, err := st.ReadState()
	if err != nil {
		// daemon 未运行或尚未写入
		fmt.Println("liquoten 未运行 | color=gray")
		fmt.Println("---")
		fmt.Println("daemon 尚未写入状态 | color=gray")
		fmt.Println("安装: launchctl load ~/Library/LaunchAgents/com.liquoten.plist | color=gray")
		return
	}
	render(cfg, balances)
}

func render(cfg config.Config, balances []providers.Balance) {
	title := menuTitle(balances)
	color := "white"
	switch {
	case anyErr(balances):
		color = "#ff453a"
	case anyOverThreshold(balances, cfg.Thresholds):
		color = "#ff9f0a"
	}
	fmt.Printf("%s | color=%s\n", title, color)
	fmt.Println("---")

	for _, b := range balances {
		if b.Err != "" {
			fmt.Printf("%s: 查询失败 | color=#ff453a\n", b.Provider)
			if msg := truncate(b.Err, 90); msg != "" {
				fmt.Printf("%s | color=gray\n", msg)
			}
			continue
		}
		for _, s := range b.Sections {
			fmt.Printf("%s | color=%s\n", sectionLine(b.Provider, s), sectionColor(b.Provider, s, cfg.Thresholds))
		}
		fmt.Printf("%s 最后更新: %s | color=gray\n", displayName(b.Provider), b.FetchedAt.Format("15:04:05"))
	}
	fmt.Println("---")
	if hasProvider(balances, "bailian") {
		fmt.Printf("打开百炼控制台 | href=%s\n", bailianConsoleURL)
	}
	if hasProvider(balances, "claudecode") {
		fmt.Printf("打开 Claude 用量页 | href=%s\n", claudeUsageURL)
	}
	fmt.Println("刷新 | refresh=true")
}

func menuTitle(balances []providers.Balance) string {
	titles := make([]string, 0, len(balances))
	for _, b := range balances {
		if b.Err != "" {
			titles = append(titles, displayName(b.Provider)+" err")
			continue
		}
		short := make([]string, 0, len(b.Sections))
		for _, s := range b.Sections {
			if s.UsedPct < 0 {
				short = append(short, shortName(s.Name)+"-")
				continue
			}
			short = append(short, fmt.Sprintf("%s %.0f%%", shortName(s.Name), s.UsedPct))
		}
		titles = append(titles, displayName(b.Provider)+" "+strings.Join(short, " · "))
	}
	if len(titles) == 0 {
		return "liquoten"
	}
	return strings.Join(titles, " · ")
}

// displayName 把注册表名映射为展示名。
func displayName(provider string) string {
	switch provider {
	case "bailian":
		return "百炼"
	case "claudecode":
		return "Claude"
	default:
		return provider
	}
}

// shortName 把 section 全名压缩成菜单栏标题用的短名。
func shortName(name string) string {
	switch name {
	case "5-hour quota":
		return "5h"
	case "1-week quota":
		return "周"
	case "1-month quota":
		return "月"
	default:
		return name
	}
}

func sectionLine(provider string, s providers.Section) string {
	line := fmt.Sprintf("%s %s: ", displayName(provider), s.Name)
	if s.UsedPct < 0 {
		line += "无限制"
	} else {
		line += fmt.Sprintf("%.1f%%", s.UsedPct)
	}
	if !s.ResetAt.IsZero() {
		line += fmt.Sprintf("（%s）", resetStr(s.ResetAt))
	}
	return line
}

func sectionColor(provider string, s providers.Section, thresholds map[string]map[string]float64) string {
	if s.UsedPct < 0 {
		return "gray"
	}
	th := 0.0
	if m, ok := thresholds[provider]; ok {
		th = m[s.Name]
	}
	switch {
	case th > 0 && s.UsedPct >= th:
		return "#ff453a"
	case s.UsedPct >= 70:
		return "#ff9f0a"
	default:
		return "white"
	}
}

// resetStr 输出距重置的剩余时间，如 "2h 15m 后重置"；已过重置点输出 "已重置"。
func resetStr(at time.Time) string {
	remaining := time.Until(at)
	if remaining <= 0 {
		return "已重置"
	}
	d := int(remaining.Hours()) / 24
	h := int(remaining.Hours()) % 24
	m := int(remaining.Minutes()) % 60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh 后重置", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm 后重置", h, m)
	default:
		return fmt.Sprintf("%dm 后重置", m)
	}
}

func anyErr(balances []providers.Balance) bool {
	for _, b := range balances {
		if b.Err != "" {
			return true
		}
	}
	return false
}

func anyOverThreshold(balances []providers.Balance, thresholds map[string]map[string]float64) bool {
	for _, b := range balances {
		if b.Err != "" {
			continue
		}
		for _, s := range b.Sections {
			if s.UsedPct < 0 {
				continue
			}
			if m, ok := thresholds[b.Provider]; ok {
				if th, ok := m[s.Name]; ok && th > 0 && s.UsedPct >= th {
					return true
				}
			}
		}
	}
	return false
}

func hasProvider(balances []providers.Balance, name string) bool {
	for _, b := range balances {
		if b.Provider == name {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
