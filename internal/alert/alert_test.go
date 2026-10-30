package alert

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

type recorder struct{ fired []string }

func (r *recorder) notify(title, msg string) error {
	r.fired = append(r.fired, title+": "+msg)
	return nil
}

func TestThresholdAlertOnceThenReset(t *testing.T) {
	r := &recorder{}
	e, err := New(map[string]map[string]float64{"bailian": {"5-hour quota": 80}}, nil, t.TempDir(), r.notify)
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

func TestRemainingLevels(t *testing.T) {
	r := &recorder{}
	e, _ := New(map[string]map[string]float64{"claudecode": {"1-week quota": 90}}, []float64{50, 20}, t.TempDir(), r.notify)
	e.now = func() time.Time { return time.Date(2026, 10, 14, 9, 0, 0, 0, time.Local) }
	reset := time.Date(2026, 10, 14, 14, 30, 0, 0, time.Local)
	check := func(pct float64) {
		t.Helper()
		bal := providers.Balance{Provider: "claudecode", FetchedAt: time.Now(),
			Sections: []providers.Section{{Name: "5-hour quota", UsedPct: pct, ResetAt: reset}}}
		if err := e.Check([]providers.Balance{bal}); err != nil {
			t.Fatal(err)
		}
	}

	check(30)
	check(55)
	check(60)
	if len(r.fired) != 1 || r.fired[0] != "额度提醒: Claude 5 小时额度剩余不到 50%（当前剩余 45%），14:30 重置" {
		t.Fatalf("fired = %q", r.fired)
	}
	check(85)
	if len(r.fired) != 2 || !strings.Contains(r.fired[1], "剩余不到 20%") {
		t.Fatalf("fired = %q", r.fired)
	}
	check(3)
	check(95)
	if len(r.fired) != 3 || !strings.Contains(r.fired[2], "剩余不到 20%") {
		t.Fatalf("新周期一次跨过两个提醒点应只发一条: %q", r.fired)
	}

	week := providers.Balance{Provider: "claudecode", FetchedAt: time.Now(),
		Sections: []providers.Section{{Name: "1-week quota", UsedPct: 92, ResetAt: time.Date(2026, 10, 18, 8, 0, 0, 0, time.Local)}}}
	if err := e.Check([]providers.Balance{week}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 4 || r.fired[3] != "额度提醒: Claude 每周额度剩余不到 10%（当前剩余 8%），10月18日 08:00 重置" {
		t.Fatalf("自定义阈值作为额外提醒点: %q", r.fired)
	}
}

func TestAlertStatePersistedAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	r := &recorder{}
	e1, _ := New(map[string]map[string]float64{"bailian": {"5-hour quota": 80}}, nil, dir, r.notify)
	bal := providers.Balance{
		Provider:  "bailian",
		FetchedAt: time.Now(),
		Sections:  []providers.Section{{Name: "5-hour quota", UsedPct: 85}},
	}
	if err := e1.Check([]providers.Balance{bal}); err != nil {
		t.Fatal(err)
	}
	// 重启后同状态不应重复告警
	e2, _ := New(map[string]map[string]float64{"bailian": {"5-hour quota": 80}}, nil, dir, r.notify)
	if err := e2.Check([]providers.Balance{bal}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 1 {
		t.Fatalf("fired = %v, want 1 (重启后不重复)", r.fired)
	}
}

func TestConsecutiveFailureAlert(t *testing.T) {
	r := &recorder{}
	e, _ := New(map[string]map[string]float64{}, nil, t.TempDir(), r.notify)
	fail := providers.Balance{Provider: "bailian", FetchedAt: time.Now(), Err: "查询失败"}

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
	e, _ := New(map[string]map[string]float64{"bailian": {"5-hour quota": 80}}, nil, t.TempDir(), nil)
	bal := providers.Balance{
		Provider:  "bailian",
		FetchedAt: time.Now(),
		Sections:  []providers.Section{{Name: "5-hour quota", UsedPct: 99}},
	}
	if err := e.Check([]providers.Balance{bal}); err != nil {
		t.Fatal(err)
	}
}

func TestResetCreditsExpiryAlert(t *testing.T) {
	dir := t.TempDir()
	r := &recorder{}
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.Local)
	e, err := New(nil, nil, dir, r.notify)
	if err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return now }
	bal := func(reset time.Time, remaining float64) providers.Balance {
		return providers.Balance{
			Provider:  "codex",
			FetchedAt: now,
			Sections:  []providers.Section{{Name: "reset-credits", UsedPct: -1, Remaining: remaining, ResetAt: reset}},
		}
	}

	thirteenHours := time.Date(2026, 10, 14, 22, 0, 0, 0, time.Local)
	if err := e.Check([]providers.Balance{bal(thirteenHours, 1)}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 0 {
		t.Fatalf("fired = %v, want 0 for 13 hours", r.fired)
	}

	elevenHours := time.Date(2026, 10, 14, 20, 0, 0, 0, time.Local)
	if err := e.Check([]providers.Balance{bal(elevenHours, 1)}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 1 || r.fired[0] != "额度提醒: Codex 有重置卡将于 20:00 过期，可在 Codex 桌面 App 里使用" {
		t.Fatalf("fired = %v", r.fired)
	}
	if err := e.Check([]providers.Balance{bal(elevenHours, 1)}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 1 {
		t.Fatalf("fired = %v, want 1 before restart", r.fired)
	}

	e2, err := New(nil, nil, dir, r.notify)
	if err != nil {
		t.Fatal(err)
	}
	e2.now = func() time.Time { return now }
	if err := e2.Check([]providers.Balance{bal(elevenHours, 1)}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 1 {
		t.Fatalf("fired = %v, want 1 after restart", r.fired)
	}

	otherExpiry := time.Date(2026, 10, 14, 20, 30, 0, 0, time.Local)
	if err := e2.Check([]providers.Balance{bal(otherExpiry, 1)}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 2 || r.fired[1] != "额度提醒: Codex 有重置卡将于 20:30 过期，可在 Codex 桌面 App 里使用" {
		t.Fatalf("fired = %v", r.fired)
	}
	if err := e2.Check([]providers.Balance{bal(elevenHours, 0)}); err != nil {
		t.Fatal(err)
	}
	if got := sectionName("reset-credits"); got != "重置卡" {
		t.Fatalf("sectionName(reset-credits) = %q", got)
	}
}

func TestAddonAndSubscriptionExpiryAlert(t *testing.T) {
	dir := t.TempDir()
	r := &recorder{}
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	e, err := New(nil, nil, dir, r.notify)
	if err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return now }
	bal := func(addonExpire, subExpire time.Time) providers.Balance {
		return providers.Balance{
			Provider:  "bailian",
			FetchedAt: now,
			Sections: []providers.Section{
				{Name: "addon-credits", UsedPct: 0, Remaining: 20000, ResetAt: addonExpire},
				{Name: "subscription", UsedPct: -1, Remaining: 3, ResetAt: subExpire},
			},
		}
	}

	outside := now.Add(73 * time.Hour)
	if err := e.Check([]providers.Balance{bal(outside, outside)}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 0 {
		t.Fatalf("fired = %v, want 0 outside 3 days", r.fired)
	}

	inside := now.Add(63 * time.Hour)
	if err := e.Check([]providers.Balance{bal(inside, inside)}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 2 {
		t.Fatalf("fired = %v, want addon + subscription", r.fired)
	}
	if r.fired[0] != "额度提醒: 百炼加购额度将于 11月17日 00:00 过期，剩余 20000 Credits" {
		t.Fatalf("addon message = %q", r.fired[0])
	}
	if r.fired[1] != "额度提醒: 百炼 Token Plan 套餐将于 11月17日 00:00 到期" {
		t.Fatalf("subscription message = %q", r.fired[1])
	}
	if err := e.Check([]providers.Balance{bal(inside, inside)}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 2 {
		t.Fatalf("fired = %v, want no duplicates", r.fired)
	}

	e2, err := New(nil, nil, dir, r.notify)
	if err != nil {
		t.Fatal(err)
	}
	e2.now = func() time.Time { return now }
	if err := e2.Check([]providers.Balance{bal(inside, inside)}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 2 {
		t.Fatalf("fired = %v, want no duplicates after restart", r.fired)
	}

	other := now.Add(60 * time.Hour)
	if err := e2.Check([]providers.Balance{bal(other, other)}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 4 {
		t.Fatalf("fired = %v, want new expiry alerts", r.fired)
	}
	if got := sectionName("addon-credits"); got != "加购额度" {
		t.Fatalf("sectionName(addon-credits) = %q", got)
	}
	if got := sectionName("subscription"); got != "套餐" {
		t.Fatalf("sectionName(subscription) = %q", got)
	}
}

func TestAddonRemainingPercentageAlertStillWorks(t *testing.T) {
	dir := t.TempDir()
	r := &recorder{}
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	e, err := New(nil, []float64{50, 20}, dir, r.notify)
	if err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return now }
	bal := providers.Balance{
		Provider:  "bailian",
		FetchedAt: now,
		Sections: []providers.Section{{
			Name:      "addon-credits",
			UsedPct:   85,
			Remaining: 3000,
			ResetAt:   now.Add(10 * 24 * time.Hour),
		}},
	}
	if err := e.Check([]providers.Balance{bal}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 1 || !strings.Contains(r.fired[0], "加购额度剩余不到 20%") {
		t.Fatalf("fired = %v", r.fired)
	}
	if err := e.Check([]providers.Balance{bal}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 1 {
		t.Fatalf("fired = %v, want generic threshold once", r.fired)
	}
}

func TestFileNotifierAppendsEvents(t *testing.T) {
	dir := t.TempDir()
	n, err := FileNotifier(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := n("liquoten 告警", "第一条"); err != nil {
		t.Fatal(err)
	}
	if err := n("liquoten 告警", "第二条"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "notifications.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	var evs []Event
	for _, line := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("非法 JSON 行 %q: %v", line, err)
		}
		evs = append(evs, ev)
	}
	if evs[0].Body != "第一条" || evs[1].Body != "第二条" {
		t.Fatalf("bodies = %v", evs)
	}
	if evs[0].ID == evs[1].ID {
		t.Fatalf("事件 id 重复: %s", evs[0].ID)
	}
}

func TestFileNotifierThroughEngine(t *testing.T) {
	dir := t.TempDir()
	n, err := FileNotifier(dir)
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(map[string]map[string]float64{"bailian": {"5-hour quota": 80}}, nil, dir, n)
	if err != nil {
		t.Fatal(err)
	}
	bal := providers.Balance{
		Provider:  "bailian",
		FetchedAt: time.Now(),
		Sections:  []providers.Section{{Name: "5-hour quota", UsedPct: 85}},
	}
	if err := e.Check([]providers.Balance{bal}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "notifications.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "百炼 5 小时额度剩余不到 20%（当前剩余 15%）") {
		t.Fatalf("事件内容不符: %s", data)
	}
}

func TestRenewingSubscriptionNotAlerted(t *testing.T) {
	r := &recorder{}
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	e, err := New(nil, nil, t.TempDir(), r.notify)
	if err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return now }
	bal := providers.Balance{Provider: "bailian", Sections: []providers.Section{
		{Name: "subscription-renewing", UsedPct: -1, Remaining: 1, ResetAt: now.Add(24 * time.Hour)},
	}}
	if err := e.Check([]providers.Balance{bal}); err != nil {
		t.Fatal(err)
	}
	if len(r.fired) != 0 {
		t.Fatalf("fired = %v, want none for auto-renewing subscription", r.fired)
	}
}

func TestPruneExpiredAlertKeys(t *testing.T) {
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.Local)
	e, err := New(nil, nil, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return now }
	old := now.Add(-8 * 24 * time.Hour).Unix()
	recent := now.Add(-time.Hour).Unix()
	e.alerted = map[string]bool{
		"codex|reset-credits|" + itoa(old):          true,
		"bailian|addon-credits-expire|" + itoa(old): true,
		"bailian|subscription|" + itoa(recent):      true,
		"codex|1-week quota|80":                     true,
		"bailian|addon-credits|50":                  true,
	}
	if err := e.Check(nil); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"codex|reset-credits|" + itoa(old), "bailian|addon-credits-expire|" + itoa(old)} {
		if _, ok := e.alerted[k]; ok {
			t.Fatalf("stale key %q not pruned", k)
		}
	}
	for _, k := range []string{"bailian|subscription|" + itoa(recent), "codex|1-week quota|80", "bailian|addon-credits|50"} {
		if _, ok := e.alerted[k]; !ok {
			t.Fatalf("key %q should be kept", k)
		}
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
