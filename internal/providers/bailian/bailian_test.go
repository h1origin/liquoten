package bailian

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

func TestParseTokenPlanDirect(t *testing.T) {
	raw, err := filepath.Abs(filepath.Join("testdata", "tokenplan.json"))
	if err != nil {
		t.Fatal(err)
	}
	// 直接读文件内容测试解析器
	data := readFile(t, raw)
	tp, err := parseTokenPlan(data)
	if err != nil {
		t.Fatalf("parseTokenPlan: %v", err)
	}
	if tp.Per5Hour == nil || *tp.Per5Hour != 0.374 {
		t.Fatalf("Per5Hour = %v, want 0.374", tp.Per5Hour)
	}
	if tp.Per1Week == nil || *tp.Per1Week != 0.121 {
		t.Fatalf("Per1Week = %v, want 0.121", tp.Per1Week)
	}
	wantReset := time.UnixMilli(1768104000000)
	if got := epochMs(tp.Per5HourReset); !got.Equal(wantReset) {
		t.Fatalf("Per5HourReset = %v, want %v", got, wantReset)
	}
}

func TestParseTokenPlanEnvelopeAndNormalization(t *testing.T) {
	raw := []byte(`{"data":{"per5HourPercentage":0.85,"per5HourResetTime":1768104000,"per1WeekPercentage":91.5,"per1WeekResetTime":1768363200000}}`)
	tp, err := parseTokenPlan(raw)
	if err != nil {
		t.Fatalf("parseTokenPlan: %v", err)
	}
	// 信封展开
	if tp.Per5Hour == nil {
		t.Fatal("Per5Hour not unwrapped from envelope")
	}
	// [0,1] 比值 → 85%
	if got := pctOrMinus1(tp.Per5Hour); got != 85 {
		t.Fatalf("pctOrMinus1(0.85) = %v, want 85", got)
	}
	// >1 视为已是百分数
	if got := pctOrMinus1(tp.Per1Week); got != 91.5 {
		t.Fatalf("pctOrMinus1(91.5) = %v, want 91.5", got)
	}
	// 秒级时间戳自动补毫秒
	want := time.UnixMilli(1768104000000)
	if got := epochMs(tp.Per5HourReset); !got.Equal(want) {
		t.Fatalf("epochMs(seconds) = %v, want %v", got, want)
	}
}

func TestParseTokenPlanMissingFields(t *testing.T) {
	raw := []byte(`{"per5HourPercentage":0.5}`)
	tp, err := parseTokenPlan(raw)
	if err != nil {
		t.Fatalf("parseTokenPlan: %v", err)
	}
	if tp.Per1Week != nil {
		t.Fatalf("Per1Week = %v, want nil", tp.Per1Week)
	}
	if got := pctOrMinus1(tp.Per1Week); got != -1 {
		t.Fatalf("pctOrMinus1(nil) = %v, want -1", got)
	}
	if got := epochMs(tp.Per1WeekReset); !got.IsZero() {
		t.Fatalf("epochMs(nil) = %v, want zero", got)
	}
}

func TestParseTokenPlanMonthOnly(t *testing.T) {
	tp, err := parseTokenPlan(readFile(t, filepath.Join("testdata", "tokenplan-month.json")))
	if err != nil {
		t.Fatalf("parseTokenPlan: %v", err)
	}
	got := sections(tp)
	if len(got) != 1 {
		t.Fatalf("len(sections) = %d, want 1: %+v", len(got), got)
	}
	s := got[0]
	if s.Name != "1-month quota" || s.UsedPct != 0 || s.Remaining != -1 {
		t.Fatalf("section = %+v, want 1-month quota 0%%", s)
	}
	if want := time.UnixMilli(1791907200000); !s.ResetAt.Equal(want) {
		t.Fatalf("ResetAt = %v, want %v", s.ResetAt, want)
	}
}

func TestParseTokenPlanMonthEnvelope(t *testing.T) {
	tp, err := parseTokenPlan([]byte(`{"data":{"per1MonthPercentage":0.42,"per1MonthResetTime":1791907200000}}`))
	if err != nil {
		t.Fatalf("parseTokenPlan: %v", err)
	}
	got := sections(tp)
	if len(got) != 1 || got[0].UsedPct != 42 {
		t.Fatalf("sections = %+v, want 1-month quota 42%%", got)
	}
}

func TestSectionsEmptyWhenNoFields(t *testing.T) {
	tp, err := parseTokenPlan([]byte(`{}`))
	if err != nil {
		t.Fatalf("parseTokenPlan: %v", err)
	}
	if got := sections(tp); len(got) != 0 {
		t.Fatalf("sections = %+v, want empty", got)
	}
}

func TestQueryWithFakeCLI(t *testing.T) {
	p := New(providers.Options{Command: filepath.Join("testdata", "fake-bl.sh")})
	bal, err := p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if bal.Provider != Name {
		t.Fatalf("Provider = %q, want %q", bal.Provider, Name)
	}
	if len(bal.Sections) != 2 {
		t.Fatalf("len(Sections) = %d, want 2", len(bal.Sections))
	}
	s := bal.Sections[0]
	if s.Name != "5-hour quota" || s.UsedPct != 37.4 {
		t.Fatalf("Sections[0] = %+v, want 5-hour quota 37.4%%", s)
	}
	if s.ResetAt.IsZero() {
		t.Fatal("Sections[0].ResetAt is zero")
	}
	w := bal.Sections[1]
	if w.Name != "1-week quota" || w.UsedPct != 12.1 {
		t.Fatalf("Sections[1] = %+v, want 1-week quota 12.1%%", w)
	}
}

func TestQueryFailsWhenCLIMissing(t *testing.T) {
	p := New(providers.Options{Command: "definitely-not-a-real-bl-binary"})
	_, err := p.Query(context.Background())
	if err == nil {
		t.Fatal("Query should fail when bl is missing")
	}
}

func TestLoginWithFakeCLI(t *testing.T) {
	p := New(providers.Options{Command: filepath.Join("testdata", "fake-bl.sh")})
	for _, site := range []string{"domestic", "international"} {
		if err := p.Login(context.Background(), site); err != nil {
			t.Fatalf("Login(%q): %v", site, err)
		}
	}
	if err := p.Login(context.Background(), "mars"); err == nil {
		t.Fatal("Login should reject unknown site")
	}
	var _ providers.Authenticator = p
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPreflight(t *testing.T) {
	orig := searchDirs
	t.Cleanup(func() { searchDirs = orig })
	searchDirs = func() []string { return nil }
	t.Setenv("PATH", t.TempDir())

	fake, err := filepath.Abs(filepath.Join("testdata", "fake-bl.sh"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := New(providers.Options{Command: fake}).Preflight(context.Background())
	if err != nil || got != fake {
		t.Fatalf("Preflight = %q, %v; want %q", got, err, fake)
	}

	_, err = New(providers.Options{}).Preflight(context.Background())
	if !errors.Is(err, providers.ErrNotInstalled) {
		t.Fatalf("bl 缺失时应返回 ErrNotInstalled，got %v", err)
	}

	dir := t.TempDir()
	bl := filepath.Join(dir, "bl")
	if err := os.WriteFile(bl, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	searchDirs = func() []string { return []string{dir} }
	got, err = New(providers.Options{}).Preflight(context.Background())
	if err != nil || got != bl {
		t.Fatalf("应在常见安装目录找到 bl: %q, %v", got, err)
	}
}

func TestInstallWithoutNpm(t *testing.T) {
	orig := searchDirs
	t.Cleanup(func() { searchDirs = orig })
	searchDirs = func() []string { return nil }
	t.Setenv("PATH", t.TempDir())
	err := New(providers.Options{}).Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Node.js") {
		t.Fatalf("没有 npm 时应提示安装 Node.js，got %v", err)
	}
}
