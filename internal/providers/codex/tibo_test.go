package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestParseTiboFixture(t *testing.T) {
	got, err := parseTibo(testdata(t, "tibo-resets.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 28, 2, 42, 53, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v (banked grants ignored)", got, want)
	}
}

func TestParseTiboRejectsGarbage(t *testing.T) {
	if _, err := parseTibo([]byte("<html>")); err == nil {
		t.Fatal("want error")
	}
}

func TestTiboSectionsAndFrequency(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	now := t0
	var mu sync.Mutex
	calls := 0
	body := testdata(t, "tibo-resets.json")
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
			if s.Name == "tibo-reset" {
				if s.UsedPct != -1 || s.Remaining != -1 {
					t.Fatalf("%s = %+v, want -1 fields", s.Name, s)
				}
				out[s.Name] = s.ResetAt
			}
		}
		return out
	}
	got := query()
	if len(got) != 1 || !got["tibo-reset"].Equal(time.Date(2026, 10, 28, 2, 42, 53, 0, time.UTC)) {
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

func TestTiboFailureKeepsLastResult(t *testing.T) {
	fail := false
	body := testdata(t, "tibo-resets.json")
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
	p.refreshTibo(context.Background(), time.Now())
	fail = true
	p.RefreshNow()
	p.refreshTibo(context.Background(), time.Now())
	if len(p.tiboSections()) != 1 {
		t.Fatalf("sections = %v, want previous result kept", p.tiboSections())
	}
}

func TestTiboDisabledWithoutFeedURL(t *testing.T) {
	p := New(fixtureHome(t))
	p.refreshTibo(context.Background(), time.Now())
	if p.tiboSections() != nil || !p.lastTiboAttempt.IsZero() {
		t.Fatal("feed should be disabled when feedURL is empty")
	}
}

func TestTiboRetriesOnceThenSkips(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	calls, fail := 0, true
	body := testdata(t, "tibo-resets.json")
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
		p.refreshTibo(context.Background(), t0.Add(at))
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
	if len(p.tiboSections()) != 1 {
		t.Fatalf("sections = %v, want loaded after successful retry", p.tiboSections())
	}
	step(17*time.Minute, 4)
	step(30*time.Minute, 5)
}

func TestTiboTimeout(t *testing.T) {
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
	p.tiboTimeout = 50 * time.Millisecond
	start := time.Now()
	p.refreshTibo(context.Background(), start)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("elapsed = %v, want timeout", elapsed)
	}
	if p.tiboRetryAt.IsZero() || p.tiboLoaded {
		t.Fatal("timeout should schedule a retry and load nothing")
	}
	if tiboTimeout != 5*time.Second || New("").tiboTimeout != tiboTimeout {
		t.Fatalf("default timeout = %v", tiboTimeout)
	}
}
