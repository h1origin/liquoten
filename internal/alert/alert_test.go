package alert

import (
	"testing"
	"time"

	"github.com/lk/liquoten-cred/internal/providers"
)

type recorder struct{ fired []string }

func (r *recorder) notify(title, msg string) error {
	r.fired = append(r.fired, title+": "+msg)
	return nil
}

func TestThresholdAlertOnceThenReset(t *testing.T) {
	r := &recorder{}
	e, err := New(map[string]map[string]float64{"bailian": {"5-hour quota": 80}}, t.TempDir(), r.notify)
	if err != nil {
		t.Fatal(err)
	}
	bal := func(pct float64) providers.Balance {
		return providers.Balance{
			Provider:  "bailian",
			FetchedAt: time.Now(),
			Sections:  []providers.Section{{Name: "5-hour quota", UsedPct: pct}},
		}
	}

	if err := e.Check([]providers.Balance{bal(85)}); err != nil { // 升穿 → 告警
		t.Fatal(err)
	}
	if err := e.Check([]providers.Balance{bal(90)}); err != nil { // 仍在阈值上 → 不重复
		t.Fatal(err)
	}
	if len(r.fired) != 1 {
		t.Fatalf("fired = %v, want 1", r.fired)
	}
	if err := e.Check([]providers.Balance{bal(50)}); err != nil { // 回落 → 复位
		t.Fatal(err)
	}
	if err := e.Check([]providers.Balance{bal(85)}); err != nil { // 再次升穿 → 再告警
		t.Fatal(err)
	}
	if len(r.fired) != 2 {
		t.Fatalf("fired = %v, want 2", r.fired)
	}
}

func TestAlertStatePersistedAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	r := &recorder{}
	e1, _ := New(map[string]map[string]float64{"bailian": {"5-hour quota": 80}}, dir, r.notify)
	bal := providers.Balance{
		Provider:  "bailian",
		FetchedAt: time.Now(),
		Sections:  []providers.Section{{Name: "5-hour quota", UsedPct: 85}},
	}
	if err := e1.Check([]providers.Balance{bal}); err != nil {
		t.Fatal(err)
	}
	// 重启后同状态不应重复告警
	e2, _ := New(map[string]map[string]float64{"bailian": {"5-hour quota": 80}}, dir, r.notify)
	if err := e2.Check([]providers.Balance{bal}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 1 {
		t.Fatalf("fired = %v, want 1 (重启后不重复)", r.fired)
	}
}

func TestConsecutiveFailureAlert(t *testing.T) {
	r := &recorder{}
	e, _ := New(map[string]map[string]float64{}, t.TempDir(), r.notify)
	fail := providers.Balance{Provider: "bailian", FetchedAt: time.Now(), Err: "bl not found"}

	for i := 1; i <= 2; i++ {
		if err := e.Check([]providers.Balance{fail}); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.fired) != 0 {
		t.Fatalf("fired = %v, want 0 before 3rd failure", r.fired)
	}
	if err := e.Check([]providers.Balance{fail}); err != nil { // 第 3 次 → 告警
		t.Fatal(err)
	}
	if len(r.fired) != 1 {
		t.Fatalf("fired = %v, want 1", r.fired)
	}
	if err := e.Check([]providers.Balance{fail}); err != nil { // 第 4 次 → 不重复
		t.Fatal(err)
	}
	if len(r.fired) != 1 {
		t.Fatalf("fired = %v, want 1 (连续失败不重复)", r.fired)
	}
	// 恢复成功 → 失败计数复位
	ok := providers.Balance{Provider: "bailian", FetchedAt: time.Now()}
	if err := e.Check([]providers.Balance{ok}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if err := e.Check([]providers.Balance{fail}); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.fired) != 1 {
		t.Fatalf("fired = %v, want 1 (计数已复位)", r.fired)
	}
}

func TestNilNotifierSilent(t *testing.T) {
	e, _ := New(map[string]map[string]float64{"bailian": {"5-hour quota": 80}}, t.TempDir(), nil)
	bal := providers.Balance{
		Provider:  "bailian",
		FetchedAt: time.Now(),
		Sections:  []providers.Section{{Name: "5-hour quota", UsedPct: 99}},
	}
	if err := e.Check([]providers.Balance{bal}); err != nil {
		t.Fatal(err)
	}
}
