package config

import (
	"os"
	"path/filepath"
	"slices"
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
	if !slices.Equal(cfg.AlertRemaining, []float64{50, 20}) {
		t.Fatalf("默认提醒点 = %v, want [50 20]", cfg.AlertRemaining)
	}
	if len(cfg.Thresholds) != 0 {
		t.Fatalf("默认不应有额外阈值: %v", cfg.Thresholds)
	}
}

func TestLoadUserConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
poll_interval = "10m"

[providers.bailian]
api_key = "sk-test"
access_key_id = "LTAI-test"
access_key_secret = "secret-test"
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
	if cfg.Providers["bailian"].AccessKeyID != "LTAI-test" || cfg.Providers["bailian"].AccessKeySecret != "secret-test" {
		t.Fatalf("AccessKey = %q / %q", cfg.Providers["bailian"].AccessKeyID, cfg.Providers["bailian"].AccessKeySecret)
	}
	if !cfg.Providers["bailian"].IsEnabled() {
		t.Fatal("显式出现但未设 enabled 的 provider 应默认启用")
	}
	if got := cfg.Thresholds["bailian"]["5-hour quota"]; got != 60 {
		t.Fatalf("5-hour 阈值 = %v, want 60", got)
	}
}

func TestLoadIgnoresLegacyCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `[providers.bailian]
command = "/legacy/path"
access_key_id = "LTAI-test"
access_key_secret = "secret-test"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("残留 command 行不应导致加载失败: %v", err)
	}
	pc := cfg.Providers["bailian"]
	if pc.AccessKeyID != "LTAI-test" || pc.AccessKeySecret != "secret-test" {
		t.Fatalf("AccessKey = %q / %q", pc.AccessKeyID, pc.AccessKeySecret)
	}
	if !pc.IsEnabled() {
		t.Fatal("bailian 应启用")
	}
}

func TestAlertRemaining(t *testing.T) {
	dir := t.TempDir()
	for content, want := range map[string][]float64{
		"alert_remaining = [30.0]\n": {30},
		"alert_remaining = []\n":     {},
		"notify = \"app\"\n":         {50, 20},
	} {
		path := filepath.Join(dir, "config.toml")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(cfg.AlertRemaining, want) {
			t.Fatalf("%q: AlertRemaining = %v, want %v", content, cfg.AlertRemaining, want)
		}
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

func TestSetProviderAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `[providers.qodercn]
enabled = false
timeout = "30s"

[thresholds.qodercn]
"1-month quota" = 80.0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	on, key := true, `pt-a"b\c`
	if err := SetProvider(path, "qodercn", ProviderUpdate{Enabled: &on, APIKey: &key}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	pc := cfg.Providers["qodercn"]
	if !pc.IsEnabled() || pc.APIKey != key || pc.Timeout != 30*time.Second {
		t.Fatalf("qodercn = %+v", pc)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("含 api_key 的配置应为 0600: %v %v", st.Mode().Perm(), err)
	}

	off := false
	if err := SetProvider(path, "qodercn", ProviderUpdate{Enabled: &off, Delete: []string{"api_key"}}); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if pc := cfg.Providers["qodercn"]; pc.IsEnabled() || pc.APIKey != "" {
		t.Fatalf("关闭后应禁用并删除 api_key: %+v", pc)
	}
	if got := cfg.Thresholds["qodercn"]["1-month quota"]; got != 80 {
		t.Fatalf("阈值被破坏: %v", got)
	}
}

func TestSetProviderAccessKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `# top comment
[providers.bailian]
enabled = true
timeout = "30s"
# keep this comment

[thresholds.bailian]
"5-hour quota" = 70.0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	id, secret := "LTAI-first", "secret-first"
	if err := SetProvider(path, "bailian", ProviderUpdate{AccessKeyID: &id, AccessKeySecret: &secret}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	pc := cfg.Providers["bailian"]
	if pc.AccessKeyID != id || pc.AccessKeySecret != secret {
		t.Fatalf("AccessKey = %q / %q", pc.AccessKeyID, pc.AccessKeySecret)
	}
	if !pc.IsEnabled() || pc.Timeout != 30*time.Second {
		t.Fatalf("其余配置被破坏: %+v", pc)
	}

	id, secret = "LTAI-second", "secret-second"
	if err := SetProvider(path, "bailian", ProviderUpdate{AccessKeyID: &id, AccessKeySecret: &secret}); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	pc = cfg.Providers["bailian"]
	if pc.AccessKeyID != id || pc.AccessKeySecret != secret {
		t.Fatalf("覆盖后 AccessKey = %q / %q", pc.AccessKeyID, pc.AccessKeySecret)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "access_key_id = ") != 1 || strings.Count(text, "access_key_secret = ") != 1 {
		t.Fatalf("覆盖时不应重复写入:\n%s", text)
	}
	if !strings.Contains(text, "# top comment") || !strings.Contains(text, "# keep this comment") {
		t.Fatalf("注释应保留:\n%s", text)
	}

	if err := SetProvider(path, "bailian", ProviderUpdate{Delete: []string{"access_key_id", "access_key_secret"}}); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	pc = cfg.Providers["bailian"]
	if pc.AccessKeyID != "" || pc.AccessKeySecret != "" {
		t.Fatalf("删除后 AccessKey 仍存在: %q / %q", pc.AccessKeyID, pc.AccessKeySecret)
	}
	if !pc.IsEnabled() || pc.Timeout != 30*time.Second {
		t.Fatalf("删除后其余配置被破坏: %+v", pc)
	}
	if got := cfg.Thresholds["bailian"]["5-hour quota"]; got != 70 {
		t.Fatalf("阈值被破坏: %v", got)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text = string(data)
	if strings.Contains(text, "access_key_id") || strings.Contains(text, "access_key_secret") {
		t.Fatalf("删除后仍残留 AccessKey 键:\n%s", text)
	}
	if !strings.Contains(text, "# keep this comment") {
		t.Fatalf("删除后注释应保留:\n%s", text)
	}
}
