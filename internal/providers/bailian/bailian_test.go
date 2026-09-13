package bailian

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lk/liquoten-cred/internal/providers"
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

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
