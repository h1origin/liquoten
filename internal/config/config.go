// Package config 负责加载 ~/.config/llmcred/config.toml 与默认值合并。
package config

import (
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	// DefaultPollInterval 是 daemon 默认轮询间隔（实际带 ±10% 抖动，约 9–11 分钟）。
	DefaultPollInterval = 10 * time.Minute
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
	Providers    map[string]ProviderCfg        `toml:"providers"`
	Thresholds   map[string]map[string]float64 `toml:"thresholds"`
}

// Default 返回内置默认配置：百炼启用，5 小时窗口 80%、周窗口 90% 告警。
func Default() Config {
	return Config{
		PollInterval: DefaultPollInterval,
		Providers: map[string]ProviderCfg{
			"bailian": {Enabled: boolPtr(true)},
		},
		Thresholds: map[string]map[string]float64{
			"bailian": {
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
	if p := os.Getenv("LLMCRED_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return home + "/.config/llmcred/config.toml"
}

func boolPtr(b bool) *bool { return &b }
