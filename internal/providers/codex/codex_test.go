package codex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	data, err := os.ReadFile(filepath.Join("testdata", "home", "sessions", "2026", "10", "13", "rollout-weekly.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "sessions", "2026", "10", "13", "rollout-weekly.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 13, 1, 43, 0, 0, time.UTC)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeTestAuth(t *testing.T, home, token, account string) {
	t.Helper()
	data := fmt.Sprintf(`{"tokens":{"access_token":%q,"account_id":%q}}`, token, account)
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func guardConsume(t *testing.T, r *http.Request) {
	t.Helper()
	if strings.Contains(r.URL.Path, "/consume") {
		t.Fatal("测试不允许请求重置卡 consume 接口")
	}
}

func resetSection(t *testing.T, sections []providers.Section) providers.Section {
	t.Helper()
	for _, s := range sections {
		if s.Name == "reset-credits" {
			return s
		}
	}
	t.Fatalf("sections = %+v, want reset-credits", sections)
	return providers.Section{}
}

func TestLiveUsageMapping(t *testing.T) {
	home := t.TempDir()
	writeTestAuth(t, home, "token-1", "account-1")
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	usageBody := testdata(t, "wham-usage.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guardConsume(t, r)
		if r.URL.Path != usagePath {
			http.NotFound(w, r)
			return
		}
		w.Write(usageBody)
	}))
	defer server.Close()

	p := newProvider(home, 0)
	p.now = func() time.Time { return now }
	p.lastResetAttempt = now
	p.baseURL = server.URL
	bal, err := p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !bal.FetchedAt.Equal(now) {
		t.Fatalf("FetchedAt = %v, want %v", bal.FetchedAt, now)
	}
	if len(bal.Sections) != 2 {
		t.Fatalf("Sections = %+v, want week + credits", bal.Sections)
	}
	w := bal.Sections[0]
	if w.Name != "1-week quota" || w.UsedPct != 53 || w.Remaining != -1 {
		t.Fatalf("week = %+v", w)
	}
	if !w.ResetAt.Equal(time.Unix(1793781560, 0)) {
		t.Fatalf("ResetAt = %v", w.ResetAt)
	}
	c := bal.Sections[1]
	if c.Name != "credits" || c.UsedPct != -1 || c.Remaining != 62500 {
		t.Fatalf("credits = %+v", c)
	}
}

func TestUsageDataSelection(t *testing.T) {
	localTime := time.Date(2026, 10, 12, 17, 43, 3, 505000000, time.UTC)
	usageBody := testdata(t, "wham-usage.json")
	cases := []struct {
		name        string
		home        string
		now         time.Time
		wantPct     float64
		wantFetched time.Time
	}{
		{"local newer", tempHome(t), time.Date(2026, 10, 12, 17, 0, 0, 0, time.UTC), 6, localTime},
		{"live newer", tempHome(t), time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), 53, time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)},
		{"no local", t.TempDir(), time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), 53, time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writeTestAuth(t, c.home, "token-1", "account-1")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				guardConsume(t, r)
				if r.URL.Path != usagePath {
					http.NotFound(w, r)
					return
				}
				w.Write(usageBody)
			}))
			defer server.Close()

			p := newProvider(c.home, 0)
			p.now = func() time.Time { return c.now }
			p.lastResetAttempt = c.now
			p.baseURL = server.URL
			bal, err := p.Query(context.Background())
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(bal.Sections) == 0 || bal.Sections[0].UsedPct != c.wantPct {
				t.Fatalf("Sections = %+v, want first pct %v", bal.Sections, c.wantPct)
			}
			if !bal.FetchedAt.Equal(c.wantFetched) {
				t.Fatalf("FetchedAt = %v, want %v", bal.FetchedAt, c.wantFetched)
			}
		})
	}
}

func TestLiveUsageFrequency(t *testing.T) {
	home := t.TempDir()
	writeTestAuth(t, home, "token-1", "account-1")
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	now := t0
	var mu sync.Mutex
	calls := 0
	usageBody := testdata(t, "wham-usage.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guardConsume(t, r)
		if r.URL.Path != usagePath {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		calls++
		mu.Unlock()
		w.Write(usageBody)
	}))
	defer server.Close()

	p := newProvider(home, 0)
	p.now = func() time.Time { return now }
	p.lastResetAttempt = t0
	p.baseURL = server.URL
	query := func() {
		t.Helper()
		if _, err := p.Query(context.Background()); err != nil {
			t.Fatalf("Query: %v", err)
		}
	}
	query()
	now = t0.Add(15 * time.Minute)
	query()
	mu.Lock()
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 within 30 minutes", calls)
	}
	mu.Unlock()
	now = t0.Add(30 * time.Minute)
	query()
	mu.Lock()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 after 30 minutes", calls)
	}
	mu.Unlock()
}

func TestResetCreditsCachingAndExpiry(t *testing.T) {
	home := tempHome(t)
	writeTestAuth(t, home, "token-1", "account-1")
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	now := t0
	var mu sync.Mutex
	resetCalls := 0
	resetBody := testdata(t, "wham-reset-credits.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guardConsume(t, r)
		if r.URL.Path != resetCreditsPath {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		resetCalls++
		mu.Unlock()
		w.Write(resetBody)
	}))
	defer server.Close()

	p := newProvider(home, 0)
	p.now = func() time.Time { return now }
	p.baseURL = server.URL
	check := func(wantCount int, wantReset time.Time, wantCalls int) {
		t.Helper()
		p.lastUsageAttempt = now
		bal, err := p.Query(context.Background())
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		sec := resetSection(t, bal.Sections)
		if int(sec.Remaining) != wantCount {
			t.Fatalf("remaining = %v, want %d", sec.Remaining, wantCount)
		}
		if wantReset.IsZero() {
			if !sec.ResetAt.IsZero() {
				t.Fatalf("ResetAt = %v, want zero", sec.ResetAt)
			}
		} else if !sec.ResetAt.Equal(wantReset) {
			t.Fatalf("ResetAt = %v, want %v", sec.ResetAt, wantReset)
		}
		mu.Lock()
		if resetCalls != wantCalls {
			t.Fatalf("reset calls = %d, want %d", resetCalls, wantCalls)
		}
		mu.Unlock()
	}

	firstExpiry := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	secondExpiry := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	check(2, firstExpiry, 1)
	now = t0.Add(30 * time.Minute)
	check(2, firstExpiry, 1)
	now = t0.Add(6 * time.Hour)
	check(2, firstExpiry, 2)
	now = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	check(1, secondExpiry, 3)
	now = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	check(0, time.Time{}, 4)
}

func TestNoAuthSkipsLiveRequests(t *testing.T) {
	for _, name := range []string{"no auth file", "empty access token"} {
		t.Run(name, func(t *testing.T) {
			home := tempHome(t)
			if name == "empty access token" {
				writeTestAuth(t, home, "", "account-1")
			}
			var mu sync.Mutex
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				guardConsume(t, r)
				mu.Lock()
				calls++
				mu.Unlock()
				http.NotFound(w, r)
			}))
			defer server.Close()

			p := newProvider(home, 0)
			p.now = func() time.Time { return time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC) }
			p.baseURL = server.URL
			bal, err := p.Query(context.Background())
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(bal.Sections) != 2 {
				t.Fatalf("Sections = %+v, want local data", bal.Sections)
			}
			mu.Lock()
			if calls != 0 {
				t.Fatalf("calls = %d, want 0 without auth", calls)
			}
			mu.Unlock()
		})
	}
}

func TestLive401FallsBackToLocalAndBacksOff(t *testing.T) {
	home := tempHome(t)
	writeTestAuth(t, home, "token-1", "account-1")
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	now := t0
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guardConsume(t, r)
		if r.URL.Path != usagePath {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"email":"user@example.com","user_id":"user-123","access_token":"token-1"}`))
	}))
	defer server.Close()

	p := newProvider(home, 0)
	p.now = func() time.Time { return now }
	p.lastResetAttempt = t0
	p.baseURL = server.URL
	bal, err := p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query should ignore live failure: %v", err)
	}
	if len(bal.Sections) == 0 || bal.Sections[0].UsedPct != 6 {
		t.Fatalf("Sections = %+v, want local weekly 6%%", bal.Sections)
	}
	now = t0.Add(15 * time.Minute)
	if _, err := p.Query(context.Background()); err != nil {
		t.Fatalf("Query: %v", err)
	}
	mu.Lock()
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 after 401 within 30 minutes", calls)
	}
	mu.Unlock()
	now = t0.Add(30 * time.Minute)
	if _, err := p.Query(context.Background()); err != nil {
		t.Fatalf("Query: %v", err)
	}
	mu.Lock()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 after 401 at 30 minutes", calls)
	}
	mu.Unlock()
}

func TestLiveRequestHeadersAndTokenReload(t *testing.T) {
	home := t.TempDir()
	writeTestAuth(t, home, "token-1", "account-1")
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	now := t0
	var mu sync.Mutex
	calls := 0
	authorizations := []string{}
	accounts := []string{}
	agents := []string{}
	usageBody := testdata(t, "wham-usage.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guardConsume(t, r)
		if r.URL.Path != usagePath {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		calls++
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		accounts = append(accounts, r.Header.Get("ChatGPT-Account-Id"))
		agents = append(agents, r.Header.Get("User-Agent"))
		mu.Unlock()
		w.Write(usageBody)
	}))
	defer server.Close()

	p := newProvider(home, 0)
	p.now = func() time.Time { return now }
	p.lastResetAttempt = t0
	p.baseURL = server.URL
	if _, err := p.Query(context.Background()); err != nil {
		t.Fatalf("Query: %v", err)
	}
	now = t0.Add(30 * time.Minute)
	writeTestAuth(t, home, "token-2", "account-2")
	if _, err := p.Query(context.Background()); err != nil {
		t.Fatalf("Query: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if authorizations[0] != "Bearer token-1" || accounts[0] != "account-1" {
		t.Fatalf("first headers = %q / %q", authorizations[0], accounts[0])
	}
	if authorizations[1] != "Bearer token-2" || accounts[1] != "account-2" {
		t.Fatalf("second headers = %q / %q", authorizations[1], accounts[1])
	}
	if agents[0] != "liquoten" {
		t.Fatalf("User-Agent = %q", agents[0])
	}
}

func TestLiveErrorDoesNotLeakSecrets(t *testing.T) {
	home := t.TempDir()
	writeTestAuth(t, home, "super-secret-token", "secret-account")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guardConsume(t, r)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"email":"user@example.com","user_id":"user-123","access_token":"super-secret-token"}`))
	}))
	defer server.Close()

	p := newProvider(home, 0)
	p.baseURL = server.URL
	var out liveUsageResponse
	err := p.getJSON(context.Background(), usagePath, &out)
	if err == nil {
		t.Fatal("getJSON should fail")
	}
	for _, secret := range []string{"super-secret-token", "secret-account", "user@example.com", "user-123"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks %q: %v", secret, err)
		}
	}
}

func TestRefreshNowClearsAttempts(t *testing.T) {
	p := newProvider(t.TempDir(), 0)
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	p.lastUsageAttempt = now
	p.lastResetAttempt = now
	p.RefreshNow()
	if !p.lastUsageAttempt.IsZero() || !p.lastResetAttempt.IsZero() {
		t.Fatalf("attempts = %v / %v, want zero", p.lastUsageAttempt, p.lastResetAttempt)
	}
	var _ providers.Refresher = p
}

func TestLiveUsageCorrectsResetCreditCount(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	resetBody := testdata(t, "wham-reset-credits.json")
	cases := []struct {
		name       string
		usage      string
		resetCalls int
	}{
		{"count changes", "wham-usage-reset1.json", 1},
		{"count equal", "wham-usage.json", 0},
		{"count missing", "wham-usage-no-reset-credits.json", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			writeTestAuth(t, home, "token-1", "account-1")
			usageBody := testdata(t, c.usage)
			var mu sync.Mutex
			resetCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				guardConsume(t, r)
				switch r.URL.Path {
				case usagePath:
					w.Write(usageBody)
				case resetCreditsPath:
					mu.Lock()
					resetCalls++
					mu.Unlock()
					w.Write(resetBody)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			p := newProvider(home, 0)
			p.now = func() time.Time { return now }
			p.baseURL = server.URL
			p.resetCredits = []resetCredit{
				{Status: "available", IsSupportedByPlan: true, ExpiresAt: now.Add(24 * time.Hour)},
				{Status: "available", IsSupportedByPlan: true, ExpiresAt: now.Add(48 * time.Hour)},
			}
			p.resetCreditsLoaded = true
			p.lastResetAttempt = now
			if _, err := p.Query(context.Background()); err != nil {
				t.Fatalf("Query: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if resetCalls != c.resetCalls {
				t.Fatalf("reset calls = %d, want %d", resetCalls, c.resetCalls)
			}
		})
	}
}

func TestResetCountChangedUsesServerHistory(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	credits := []resetCredit{
		{Status: "available", IsSupportedByPlan: true, ExpiresAt: now.Add(time.Hour)},
	}
	two, one := 2, 1
	if !resetCountChanged(nil, 2, credits, now) {
		t.Fatal("first observation differing from local count should refetch")
	}
	if resetCountChanged(&two, 2, credits, now) {
		t.Fatal("unchanged server count should not refetch even if local count differs")
	}
	if !resetCountChanged(&two, 1, credits, now) {
		t.Fatal("server count change should refetch")
	}
	if resetCountChanged(&one, 1, credits, now) {
		t.Fatal("equal counts should not refetch")
	}
}
