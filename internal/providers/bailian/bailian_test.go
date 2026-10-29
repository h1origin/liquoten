package bailian

import (
	"context"
	"os"
	"path/filepath"
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

func TestQueryWithoutAccessKey(t *testing.T) {
	p := New(providers.Options{})
	_, err := p.Query(context.Background())
	if err == nil || err.Error() != "未配置阿里云 AccessKey，请在服务页填写" {
		t.Fatalf("error = %v", err)
	}
}

func TestPollInterval(t *testing.T) {
	p := New(providers.Options{})
	if got := p.PollInterval(); got != 10*time.Minute {
		t.Fatalf("PollInterval = %v, want 10m", got)
	}
	var _ providers.Scheduled = p
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
