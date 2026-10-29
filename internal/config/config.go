// Package config 负责加载 ~/.config/liquoten/config.toml 与默认值合并。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	Enabled         *bool         `toml:"enabled"`
	APIKey          string        `toml:"api_key"`
	AccessKeyID     string        `toml:"access_key_id"`
	AccessKeySecret string        `toml:"access_key_secret"`
	Command         string        `toml:"command"`
	Timeout         time.Duration `toml:"timeout"`
}

// IsEnabled 返回是否启用：未显式设置时默认启用。
func (c ProviderCfg) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// Config 是完整的运行配置。
type Config struct {
	PollInterval time.Duration `toml:"poll_interval"`
	Notify       string        `toml:"notify"` // "app"（默认）或 "osascript"
	// AlertRemaining 是剩余百分比提醒点，对所有额度生效；设为 [] 关闭。
	AlertRemaining []float64                     `toml:"alert_remaining"`
	Providers      map[string]ProviderCfg        `toml:"providers"`
	Thresholds     map[string]map[string]float64 `toml:"thresholds"`
}

// Default 返回内置默认配置：不启用任何 provider（由用户在配置或 App 里选择），
// 每项额度剩余 50%、20% 时各提醒一次，通知写入 notifications.jsonl 由 Liquoten.app 投递。
func Default() Config {
	return Config{
		PollInterval:   DefaultPollInterval,
		Notify:         "app",
		AlertRemaining: []float64{50, 20},
		Providers:      map[string]ProviderCfg{},
		Thresholds:     map[string]map[string]float64{},
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
	md, err := toml.Decode(string(data), &user)
	if err != nil {
		return cfg, err
	}
	if md.IsDefined("alert_remaining") {
		cfg.AlertRemaining = user.AlertRemaining
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
		if u.AccessKeyID != "" {
			base.AccessKeyID = u.AccessKeyID
		}
		if u.AccessKeySecret != "" {
			base.AccessKeySecret = u.AccessKeySecret
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
	return SetProvider(path, name, ProviderUpdate{Enabled: &enabled})
}

// ProviderUpdate 描述对 [providers.<name>] 的改写：非 nil 字段写入，Delete 中的键删除。
type ProviderUpdate struct {
	Enabled         *bool
	APIKey          *string
	AccessKeyID     *string
	AccessKeySecret *string
	Command         *string
	Delete          []string
}

// SetProvider 原地改写配置文件中的 [providers.<name>]，保留其余内容与注释。
// 文件可能含 api_key，统一以 0600 写入。
func SetProvider(path, name string, u ProviderUpdate) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var set [][2]string
	if u.Enabled != nil {
		set = append(set, [2]string{"enabled", fmt.Sprintf("%t", *u.Enabled)})
	}
	if u.APIKey != nil {
		set = append(set, [2]string{"api_key", tomlString(*u.APIKey)})
	}
	if u.AccessKeyID != nil {
		set = append(set, [2]string{"access_key_id", tomlString(*u.AccessKeyID)})
	}
	if u.AccessKeySecret != nil {
		set = append(set, [2]string{"access_key_secret", tomlString(*u.AccessKeySecret)})
	}
	if u.Command != nil {
		set = append(set, [2]string{"command", tomlString(*u.Command)})
	}
	header := regexp.MustCompile(`^\s*\[\s*providers\.` + regexp.QuoteMeta(name) + `\s*\]\s*(#.*)?$`)
	table := regexp.MustCompile(`^\s*\[`)
	keyLine := func(key string) *regexp.Regexp { return regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`) }

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
		if len(set) == 0 {
			return nil
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "[providers."+name+"]")
		for _, kv := range set {
			lines = append(lines, kv[0]+" = "+kv[1])
		}
	} else {
		end := start + 1
		for end < len(lines) && !table.MatchString(lines[end]) {
			end++
		}
		body := slices.Clone(lines[start+1 : end])
		for _, key := range u.Delete {
			re := keyLine(key)
			body = slices.DeleteFunc(body, re.MatchString)
		}
		var added []string
		for _, kv := range set {
			re := keyLine(kv[0])
			i := slices.IndexFunc(body, re.MatchString)
			if i >= 0 {
				body[i] = kv[0] + " = " + kv[1]
			} else {
				added = append(added, kv[0]+" = "+kv[1])
			}
		}
		body = append(added, body...)
		lines = append(append(slices.Clone(lines[:start+1]), body...), lines[end:]...)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func tomlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(s) + `"`
}
