package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestParseOfficialFixture(t *testing.T) {
	got, err := parseOfficial(testdata(t, "official-resets.json"))
	if err != nil {
		t.Fatal(err)
	}
	wantGlobal := time.Date(2026, 10, 28, 2, 42, 53, 0, time.UTC)
	wantBanked := time.Date(2026, 10, 7, 19, 19, 17, 0, time.UTC)
	if !got.global.Equal(wantGlobal) || !got.banked.Equal(wantBanked) {
		t.Fatalf("got %v / %v, want %v / %v", got.global, got.banked, wantGlobal, wantBanked)
	}
}

func TestParseOfficialRejectsGarbage(t *testing.T) {
	if _, err := parseOfficial([]byte("<html>")); err == nil {
		t.Fatal("want error")
	}
}

func TestOfficialSectionsAndFrequency(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	now := t0
	var mu sync.Mutex
	calls := 0
	body := testdata(t, "official-resets.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Write(body)
	}))
	defer server.Close()

	p := New(fixtureHome(t))
	p.now = func() time.Time { return now }
	p.feedURL = server.URL
	query := func() map[string]time.Time {
		t.Helper()
		bal, err := p.Query(context.Background())
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		out := map[string]time.Time{}
		for _, s := range bal.Sections {
			if s.Name == "official-reset" || s.Name == "official-reset-card" {
				if s.UsedPct != -1 || s.Remaining != -1 {
					t.Fatalf("%s = %+v, want -1 fields", s.Name, s)
				}
				out[s.Name] = s.ResetAt
			}
		}
		return out
	}
	got := query()
	if !got["official-reset"].Equal(time.Date(2026, 10, 28, 2, 42, 53, 0, time.UTC)) || got["official-reset-card"].IsZero() {
		t.Fatalf("sections = %v", got)
	}
	now = t0.Add(10 * time.Minute)
	query()
	now = t0.Add(15 * time.Minute)
	query()
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestOfficialFailureKeepsLastResult(t *testing.T) {
	fail := false
	body := testdata(t, "official-resets.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Write(body)
	}))
	defer server.Close()

	p := New(fixtureHome(t))
	p.feedURL = server.URL
	p.refreshOfficial(context.Background(), time.Now())
	fail = true
	p.RefreshNow()
	p.refreshOfficial(context.Background(), time.Now())
	if len(p.officialSections()) != 2 {
		t.Fatalf("sections = %v, want previous result kept", p.officialSections())
	}
}

func TestOfficialDisabledWithoutFeedURL(t *testing.T) {
	p := New(fixtureHome(t))
	p.refreshOfficial(context.Background(), time.Now())
	if p.officialSections() != nil || !p.lastOfficialAttempt.IsZero() {
		t.Fatal("feed should be disabled when feedURL is empty")
	}
}

func TestOfficialRetriesOnceThenSkips(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	calls, fail := 0, true
	body := testdata(t, "official-resets.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if fail {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Write(body)
	}))
	defer server.Close()

	p := New(t.TempDir())
	p.feedURL = server.URL
	step := func(at time.Duration, wantCalls int) {
		t.Helper()
		p.refreshOfficial(context.Background(), t0.Add(at))
		mu.Lock()
		defer mu.Unlock()
		if calls != wantCalls {
			t.Fatalf("at %v calls = %d, want %d", at, calls, wantCalls)
		}
	}
	step(0, 1)
	step(30*time.Second, 1)
	step(time.Minute, 2)
	step(2*time.Minute, 2)
	step(14*time.Minute, 2)
	step(15*time.Minute, 3)
	mu.Lock()
	fail = false
	mu.Unlock()
	step(16*time.Minute, 4)
	if len(p.officialSections()) != 2 {
		t.Fatalf("sections = %v, want loaded after successful retry", p.officialSections())
	}
	step(17*time.Minute, 4)
	step(30*time.Minute, 5)
}

func TestOfficialTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	p := New(t.TempDir())
	p.feedURL = server.URL
	p.officialTimeout = 50 * time.Millisecond
	start := time.Now()
	p.refreshOfficial(context.Background(), start)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("elapsed = %v, want timeout", elapsed)
	}
	if p.officialRetryAt.IsZero() || p.officialLoaded {
		t.Fatal("timeout should schedule a retry and load nothing")
	}
	if officialTimeout != 5*time.Second || New("").officialTimeout != officialTimeout {
		t.Fatalf("default timeout = %v", officialTimeout)
	}
}
