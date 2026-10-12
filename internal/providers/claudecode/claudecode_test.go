package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "statusline-input.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newAt(dir string, now time.Time) *Provider {
	p := New(dir)
	p.now = func() time.Time { return now }
	return p
}

func TestCaptureAndQuery(t *testing.T) {
	dir := t.TempDir()
	captured := time.Unix(1738400000, 0)
	in, err := Capture(fixture(t), dir, captured)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if got, want := StatusLine(in), "[Opus 5.5] 5h 24% · 周 41%"; got != want {
		t.Fatalf("StatusLine = %q, want %q", got, want)
	}

	bal, err := newAt(dir, captured.Add(time.Minute)).Query(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !bal.FetchedAt.Equal(captured) {
		t.Fatalf("FetchedAt = %v, want %v", bal.FetchedAt, captured)
	}
	if len(bal.Sections) != 2 {
		t.Fatalf("len(Sections) = %d, want 2", len(bal.Sections))
	}
	s := bal.Sections[0]
	if s.Name != "5-hour quota" || s.UsedPct != 23.5 || s.Remaining != -1 || !s.ResetAt.Equal(time.Unix(1738425600, 0)) {
		t.Fatalf("Sections[0] = %+v", s)
	}
	w := bal.Sections[1]
	if w.Name != "1-week quota" || w.UsedPct != 41.2 || !w.ResetAt.Equal(time.Unix(1738857600, 0)) {
		t.Fatalf("Sections[1] = %+v", w)
	}
}

func TestQueryResetPassedIsZero(t *testing.T) {
	dir := t.TempDir()
	if _, err := Capture(fixture(t), dir, time.Unix(1738400000, 0)); err != nil {
		t.Fatal(err)
	}
	bal, err := newAt(dir, time.Unix(1738500000, 0)).Query(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if s := bal.Sections[0]; s.UsedPct != 0 || !s.ResetAt.IsZero() {
		t.Fatalf("5h after reset = %+v, want 0%% and no reset time", s)
	}
	if w := bal.Sections[1]; w.UsedPct != 41.2 {
		t.Fatalf("week = %+v, want 41.2%%", w)
	}
}

func TestCaptureMergesAbsentWindow(t *testing.T) {
	dir := t.TempDir()
	if _, err := Capture(fixture(t), dir, time.Unix(1738400000, 0)); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"rate_limits":{"five_hour":{"used_percentage":60,"resets_at":"2025-02-01T17:00:00Z"}}}`)
	if _, err := Capture(raw, dir, time.Unix(1738401000, 0)); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	bal, err := newAt(dir, time.Unix(1738402000, 0)).Query(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if s := bal.Sections[0]; s.UsedPct != 60 || !s.ResetAt.Equal(time.Date(2025, 2, 1, 17, 0, 0, 0, time.UTC)) {
		t.Fatalf("5h = %+v, want 60%% with RFC3339 reset", s)
	}
	if w := bal.Sections[1]; w.UsedPct != 41.2 {
		t.Fatalf("week = %+v, want kept 41.2%%", w)
	}
}

func TestCaptureWithoutRateLimitsKeepsSnapshot(t *testing.T) {
	dir := t.TempDir()
	in, err := Capture([]byte(`{"model":{"display_name":"Sonnet 5.5"}}`), dir, time.Now())
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if got := StatusLine(in); got != "[Sonnet 5.5]" {
		t.Fatalf("StatusLine = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Fatalf("snapshot should not be written, stat err = %v", err)
	}
	if _, err := New(dir).Query(context.Background()); err == nil {
		t.Fatal("Query should fail without snapshot")
	}
}

func TestCaptureRejectsInvalidJSON(t *testing.T) {
	if _, err := Capture([]byte(`not json`), t.TempDir(), time.Now()); err == nil {
		t.Fatal("Capture should fail on invalid JSON")
	}
}
