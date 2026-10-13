// Package config 负责加载 ~/.config/liquoten/config.toml 与默认值合并。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultPollInterval 是 daemon 默认轮询间隔（实际带 ±10% 抖动，约 54–66 秒）。
	DefaultPollInterval = time.Minute
	// DefaultTimeout 是单次 provider 查询的默认超时。
	DefaultTimeout = 45 * time.Second
)

// ProviderCfg 是单个 provider 的配置。Enabled 用指针以区分"未设置"与"显式 false"：
// 出现在配置文件中的 provider 默认为启用，除非显式 enabled = false。
type ProviderCfg struct {
	Enabled *bool         `toml:"enabled"`
	APIKey  string        `toml:"api_key"`
	Command string        `toml:"command"`
	Timeout time.Duration `toml:"timeout"`
}

// IsEnabled 返回是否启用：未显式设置时默认启用。
func (c ProviderCfg) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// Config 是完整的运行配置。
type Config struct {
	PollInterval time.Duration                 `toml:"poll_interval"`
	Notify       string                        `toml:"notify"` // "app"（默认）或 "osascript"
	Providers    map[string]ProviderCfg        `toml:"providers"`
	Thresholds   map[string]map[string]float64 `toml:"thresholds"`
}

// Default 返回内置默认配置：不启用任何 provider（由用户在配置或 App 里选择），
// 内置各 provider 的默认告警阈值，通知写入 notifications.jsonl 由 Liquoten.app 投递。
func Default() Config {
	return Config{
		PollInterval: DefaultPollInterval,
		Notify:       "app",
		Providers:    map[string]ProviderCfg{},
		Thresholds: map[string]map[string]float64{
			"bailian": {
				"5-hour quota":  80,
				"1-week quota":  90,
				"1-month quota": 90,
			},
			"claudecode": {
				"5-hour quota": 80,
				"1-week quota": 90,
			},
		},
	}
}

// Load 读取配置文件并与默认值合并；文件不存在时返回默认配置。
// 用户显式提供的字段覆盖默认值；未提供的保持默认。
func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	var user Config
	if err := toml.Unmarshal(data, &user); err != nil {
		return cfg, err
	}

	if user.PollInterval > 0 {
		cfg.PollInterval = user.PollInterval
	}
	if user.Notify != "" {
		cfg.Notify = user.Notify
	}
	for name, u := range user.Providers {
		base, ok := cfg.Providers[name]
		if !ok {
			base = ProviderCfg{}
		}
		if u.Enabled != nil {
			base.Enabled = u.Enabled
		}
		if u.APIKey != "" {
			base.APIKey = u.APIKey
		}
		if u.Command != "" {
			base.Command = u.Command
		}
		if u.Timeout > 0 {
			base.Timeout = u.Timeout
		}
		cfg.Providers[name] = base
	}
	for name, th := range user.Thresholds {
		if len(th) > 0 {
			cfg.Thresholds[name] = th
		}
	}
	return cfg, nil
}

// DefaultPath 返回默认配置文件路径（可用环境变量覆盖，方便测试与多机部署）。
func DefaultPath() string {
	if p := os.Getenv("LIQUOTEN_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return home + "/.config/liquoten/config.toml"
}

func SetEnabled(path, name string, enabled bool) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	value := fmt.Sprintf("enabled = %t", enabled)
	header := regexp.MustCompile(`^\s*\[\s*providers\.` + regexp.QuoteMeta(name) + `\s*\]\s*(#.*)?$`)
	enabledLine := regexp.MustCompile(`^\s*enabled\s*=`)
	table := regexp.MustCompile(`^\s*\[`)

	text := strings.TrimRight(string(data), "\n")
	lines := strings.Split(text, "\n")
	if text == "" {
		lines = nil
	}
	start := -1
	for i, l := range lines {
		if header.MatchString(l) {
			start = i
			break
		}
	}
	if start < 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "[providers."+name+"]", value)
	} else {
		replaced := false
		for i := start + 1; i < len(lines) && !table.MatchString(lines[i]); i++ {
			if enabledLine.MatchString(lines[i]) {
				lines[i] = value
				replaced = true
				break
			}
		}
		if !replaced {
			lines = append(lines[:start+1], append([]string{value}, lines[start+1:]...)...)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
