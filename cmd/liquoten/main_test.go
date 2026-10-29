package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/h1origin/liquoten/internal/config"
)

func TestManageProvidersAccessKeyMaintenance(t *testing.T) {
	content := `# comment
[providers.bailian]
enabled = true
api_key = "sk-test"
access_key_id = "LTAI-old"
access_key_secret = "secret-old"
timeout = "30s"
`
	for _, cmd := range []string{"clear-access-key", "disable"} {
		t.Run(cmd, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := manageProviders(cmd, []string{"-config", path, "bailian"}); err != nil {
				t.Fatalf("manageProviders(%s): %v", cmd, err)
			}
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			pc := cfg.Providers["bailian"]
			if pc.AccessKeyID != "" || pc.AccessKeySecret != "" {
				t.Fatalf("AccessKey 未删除: %q / %q", pc.AccessKeyID, pc.AccessKeySecret)
			}
			if pc.Timeout != 30*time.Second {
				t.Fatalf("timeout 被破坏: %v", pc.Timeout)
			}
			if cmd == "disable" && (pc.IsEnabled() || pc.APIKey != "") {
				t.Fatalf("disable 应同时关闭并删除 api_key: %+v", pc)
			}
			if cmd == "clear-access-key" && (!pc.IsEnabled() || pc.APIKey != "sk-test") {
				t.Fatalf("clear-access-key 不应改动 enabled/api_key: %+v", pc)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "# comment") {
				t.Fatalf("注释被破坏:\n%s", data)
			}
		})
	}
}

func TestEnableBailianWithoutAccessKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := manageProviders("enable", []string{"-config", path, "bailian"}); err != nil {
		t.Fatalf("enable bailian without AK: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	pc, ok := cfg.Providers["bailian"]
	if !ok || !pc.IsEnabled() {
		t.Fatalf("bailian 应允许在未填 AccessKey 时开启: %+v", pc)
	}
}

func TestSetAccessKeyRequiresBothFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	stdin, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.WriteString("LTAI-only-id\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	t.Cleanup(func() {
		os.Stdin = orig
		stdin.Close()
	})
	os.Stdin = stdin

	err = manageProviders("set-access-key", []string{"-config", path, "bailian"})
	if err == nil || err.Error() != "请填写 AccessKey ID 和 AccessKey Secret" {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("验证失败不应写配置: %v", statErr)
	}
}

func TestSetAccessKeyRejectsUnsupportedProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	err := manageProviders("set-access-key", []string{"-config", path, "qoder"})
	if err == nil || !strings.Contains(err.Error(), "不支持 AccessKey") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("不支持时应保持配置不变: %v", statErr)
	}
}
