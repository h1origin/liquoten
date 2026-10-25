package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

func fixtureHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join("testdata", "home")
	set := func(rel string, at time.Time) {
		if err := os.Chtimes(filepath.Join(home, "sessions", rel), at, at); err != nil {
			t.Fatal(err)
		}
	}
	set("2026/06/25/rollout-five-hour.jsonl", time.Date(2026, 6, 25, 1, 20, 0, 0, time.UTC))
	set("2026/10/13/rollout-weekly.jsonl", time.Date(2026, 10, 13, 1, 43, 0, 0, time.UTC))
	return home
}

func TestQueryUsesNewestSessionWithRateLimits(t *testing.T) {
	p := New(fixtureHome(t))
	p.now = func() time.Time { return time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC) }
	bal, err := p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(bal.Sections) != 2 {
		t.Fatalf("sections = %+v, want 1-week + credits", bal.Sections)
	}
	w := bal.Sections[0]
	if w.Name != "1-week quota" || w.UsedPct != 6 || w.Remaining != -1 {
		t.Fatalf("week = %+v", w)
	}
	if !w.ResetAt.Equal(time.Unix(1792314455, 0)) {
		t.Fatalf("ResetAt = %v", w.ResetAt)
	}
	c := bal.Sections[1]
	if c.Name != "credits" || c.UsedPct != -1 || c.Remaining != 62500 {
		t.Fatalf("credits = %+v", c)
	}
	if want := time.Date(2026, 10, 12, 17, 43, 3, 505000000, time.UTC); !bal.FetchedAt.Equal(want) {
		t.Fatalf("FetchedAt = %v, want %v", bal.FetchedAt, want)
	}
}

func TestFiveHourAndWeeklyWindows(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "home", "sessions", "2026", "06", "25", "rollout-five-hour.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ev, err := parseLastRateLimits(f)
	if err != nil || ev == nil {
		t.Fatalf("parse: %v %v", ev, err)
	}
	now := time.Unix(1782338969-3600, 0)
	got := sections(ev.Payload.RateLimits, ev.Timestamp, now)
	if len(got) != 2 || got[0].Name != "5-hour quota" || got[1].Name != "1-week quota" || got[1].UsedPct != 3 {
		t.Fatalf("sections = %+v", got)
	}

	after := sections(ev.Payload.RateLimits, ev.Timestamp, time.Unix(1782338969+60, 0))
	if after[0].UsedPct != 0 || !after[0].ResetAt.IsZero() {
		t.Fatalf("过了重置时间应视为 0%%: %+v", after[0])
	}
}

func TestParseKeepsLastNonNullRateLimits(t *testing.T) {
	in := strings.Join([]string{
		`{"timestamp":"2026-10-12T17:00:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":1,"window_minutes":10080,"resets_at":1}}}}`,
		`{"timestamp":"2026-10-12T17:01:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":2,"window_minutes":10080,"resets_at":1}}}}`,
		`{"timestamp":"2026-10-12T17:02:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":null}}`,
		`{"timestamp":"2026-10-12T17:03:00Z","type":"response_item","payload":{"type":"message"}}`,
	}, "\n")
	ev, err := parseLastRateLimits(strings.NewReader(in))
	if err != nil || ev == nil {
		t.Fatalf("parse: %v %v", ev, err)
	}
	if got := *ev.Payload.RateLimits.Primary.UsedPercent; got != 2 {
		t.Fatalf("used = %v, want 2", got)
	}
}

func TestWindowName(t *testing.T) {
	for minutes, want := range map[int]string{300: "5-hour quota", 10080: "1-week quota", 1440: "1-day quota", 120: "2-hour quota", 45: "45-minute quota"} {
		if got := windowName(minutes); got != want {
			t.Fatalf("windowName(%d) = %q, want %q", minutes, got, want)
		}
	}
}

func TestQueryWithoutSessions(t *testing.T) {
	p := New(t.TempDir())
	if _, err := p.Query(context.Background()); err == nil || !strings.Contains(err.Error(), "尚无数据") {
		t.Fatalf("err = %v, want 尚无数据", err)
	}
}

func TestRegistered(t *testing.T) {
	if _, err := providers.New(Name, providers.Options{}); err != nil {
		t.Fatal(err)
	}
}
