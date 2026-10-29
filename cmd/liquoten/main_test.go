package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/h1origin/liquoten/internal/config"
	"github.com/h1origin/liquoten/internal/providers"
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

type plainTestProvider struct{}

func (plainTestProvider) Name() string { return "plain" }

func (plainTestProvider) Query(context.Context) (providers.Balance, error) {
	return providers.Balance{}, nil
}

type scheduledTestProvider struct {
	interval time.Duration
}

func (scheduledTestProvider) Name() string { return "scheduled" }

func (scheduledTestProvider) Query(context.Context) (providers.Balance, error) {
	return providers.Balance{}, nil
}

func (p scheduledTestProvider) PollInterval() time.Duration { return p.interval }

func TestEveryInterval(t *testing.T) {
	base := time.Minute
	cases := []struct {
		name string
		p    providers.Provider
		want time.Duration
	}{
		{"plain", plainTestProvider{}, time.Minute},
		{"longer than base", scheduledTestProvider{interval: 5 * time.Minute}, 5 * time.Minute},
		{"shorter than base", scheduledTestProvider{interval: 30 * time.Second}, 30 * time.Second},
		{"zero", scheduledTestProvider{}, time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := everyInterval(c.p, base); got != c.want {
				t.Fatalf("everyInterval = %v, want %v", got, c.want)
			}
		})
	}
}

type refreshingTestProvider struct {
	refreshed int
}

func (*refreshingTestProvider) Name() string { return "refreshing" }

func (*refreshingTestProvider) Query(context.Context) (providers.Balance, error) {
	return providers.Balance{}, nil
}

func (p *refreshingTestProvider) RefreshNow() { p.refreshed++ }

func TestHandleRefreshSignal(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	refresher := &refreshingTestProvider{}
	provs := []providers.Provider{plainTestProvider{}, refresher}
	due := map[string]time.Time{
		"plain":      now.Add(time.Hour),
		"refreshing": now.Add(time.Hour),
	}
	handleRefreshSignal(provs, due, now)
	if refresher.refreshed != 1 {
		t.Fatalf("refreshed = %d, want 1", refresher.refreshed)
	}
	for _, p := range provs {
		if got := due[p.Name()]; !got.Equal(now) {
			t.Fatalf("due[%s] = %v, want %v", p.Name(), got, now)
		}
	}
}

func TestRefreshSignalAllowed(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last time.Time
		want bool
	}{
		{"first", time.Time{}, true},
		{"within 10s", now.Add(-9 * time.Second), false},
		{"exactly 10s", now.Add(-10 * time.Second), true},
		{"after 10s", now.Add(-11 * time.Second), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := refreshSignalAllowed(c.last, now); got != c.want {
				t.Fatalf("refreshSignalAllowed = %v, want %v", got, c.want)
			}
		})
	}
}

func TestNextIntervalRetriesFailuresAtBase(t *testing.T) {
	base := time.Minute
	long := scheduledTestProvider{interval: 30 * time.Minute}
	short := scheduledTestProvider{interval: 30 * time.Second}
	cases := []struct {
		name   string
		p      providers.Provider
		failed bool
		want   time.Duration
	}{
		{"long ok", long, false, 30 * time.Minute},
		{"long failed", long, true, time.Minute},
		{"short failed", short, true, 30 * time.Second},
		{"plain failed", plainTestProvider{}, true, time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nextInterval(c.p, base, c.failed); got != c.want {
				t.Fatalf("nextInterval = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDrainSignals(t *testing.T) {
	ch := make(chan os.Signal, 1)
	ch <- syscall.SIGUSR1
	drainSignals(ch)
	select {
	case <-ch:
		t.Fatal("signal channel not drained")
	default:
	}
}
