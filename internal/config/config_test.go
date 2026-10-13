package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval != DefaultPollInterval {
		t.Fatalf("PollInterval = %v, want %v", cfg.PollInterval, DefaultPollInterval)
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("默认不应启用任何 provider: %v", cfg.Providers)
	}
	if got := cfg.Thresholds["bailian"]["5-hour quota"]; got != 80 {
		t.Fatalf("默认 5-hour 阈值 = %v, want 80", got)
	}
	if got := cfg.Thresholds["bailian"]["1-month quota"]; got != 90 {
		t.Fatalf("默认 1-month 阈值 = %v, want 90", got)
	}
}

func TestLoadUserConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
poll_interval = "10m"

[providers.bailian]
api_key = "sk-test"
timeout = "20s"

[thresholds.bailian]
"5-hour quota" = 60.0
"1-week quota" = 95.0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval != 10*time.Minute {
		t.Fatalf("PollInterval = %v, want 10m", cfg.PollInterval)
	}
	if cfg.Providers["bailian"].APIKey != "sk-test" {
		t.Fatalf("APIKey = %q", cfg.Providers["bailian"].APIKey)
	}
	if !cfg.Providers["bailian"].IsEnabled() {
		t.Fatal("显式出现但未设 enabled 的 provider 应默认启用")
	}
	if got := cfg.Thresholds["bailian"]["5-hour quota"]; got != 60 {
		t.Fatalf("5-hour 阈值 = %v, want 60", got)
	}
}

func TestExplicitDisable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[providers.bailian]\nenabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers["bailian"].IsEnabled() {
		t.Fatal("enabled = false 应禁用")
	}
}

func TestNewProviderEntryDefaultEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	// 未来新增 provider（如 deepseek）只写一个空条目也应视为启用
	if err := os.WriteFile(path, []byte("[providers.deepseek]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Providers["deepseek"].IsEnabled() {
		t.Fatal("新增 provider 空条目应默认启用")
	}
	if _, ok := cfg.Providers["bailian"]; ok {
		t.Fatal("未提及的 bailian 不应出现")
	}
}

func TestInvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("poll_interval = [not-a-duration"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("非法 TOML 应报错")
	}
}

func TestSetEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")

	if err := SetEnabled(path, "claudecode", true); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Providers["claudecode"].IsEnabled() {
		t.Fatal("新建文件后 claudecode 应启用")
	}

	content := `poll_interval = "15m"

[providers.bailian]
command = "/opt/homebrew/bin/bl"
# api_key = "sk-xxx"

# [providers.claudecode]

[thresholds.bailian]
"5-hour quota" = 70.0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(path, "bailian", false); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(path, "claudecode", true); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Providers["bailian"].IsEnabled() {
		t.Fatal("bailian 应被禁用")
	}
	if cfg.Providers["bailian"].Command != "/opt/homebrew/bin/bl" {
		t.Fatalf("bailian command 丢失: %q", cfg.Providers["bailian"].Command)
	}
	if !cfg.Providers["claudecode"].IsEnabled() {
		t.Fatal("claudecode 应启用")
	}
	if got := cfg.Thresholds["bailian"]["5-hour quota"]; got != 70 {
		t.Fatalf("阈值被破坏: %v", got)
	}

	if err := SetEnabled(path, "bailian", true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "enabled = ") != 2 || !strings.Contains(text, "[providers.bailian]\nenabled = true\n") {
		t.Fatalf("重复切换应原地改写 enabled 行:\n%s", text)
	}
	if !strings.Contains(text, "# api_key = \"sk-xxx\"") || !strings.Contains(text, "# [providers.claudecode]") {
		t.Fatalf("注释应保留:\n%s", text)
	}
}
