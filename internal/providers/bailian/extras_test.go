package bailian

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

func testAPI(t *testing.T, r *http.Request) string {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	var params struct {
		API string `json:"Api"`
	}
	if err := json.Unmarshal([]byte(r.Form.Get("params")), &params); err != nil {
		t.Fatalf("params JSON: %v", err)
	}
	if got := r.URL.Query().Get("api"); got != params.API {
		t.Errorf("query api = %q, params Api = %q", got, params.API)
	}
	switch params.API {
	case usageAPI, addonSummaryAPI, subscriptionAPI:
	default:
		t.Fatalf("unexpected Bailian Api: %q", params.API)
	}
	return params.API
}

func section(t *testing.T, sections []providers.Section, name string) providers.Section {
	t.Helper()
	for _, s := range sections {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("sections = %+v, want %s", sections, name)
	return providers.Section{}
}

func TestAddonAndSubscriptionSections(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	usageBody := readFile(t, filepath.Join("testdata", "usage-data-v2.json"))
	addonBody := readFile(t, filepath.Join("testdata", "addon-summary.json"))
	subBody := readFile(t, filepath.Join("testdata", "subscription.json"))

	var mu sync.Mutex
	tokenCalls := 0
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == tokenPath {
			mu.Lock()
			tokenCalls++
			mu.Unlock()
			fmt.Fprint(w, `{"Success":true,"cliAccessToken":"token-1"}`)
			return
		}
		if r.URL.Path != "/cli/api.json" {
			http.NotFound(w, r)
			return
		}
		api := testAPI(t, r)
		mu.Lock()
		calls[api]++
		mu.Unlock()
		switch api {
		case usageAPI:
			w.Write(usageBody)
		case addonSummaryAPI:
			w.Write(addonBody)
		case subscriptionAPI:
			w.Write(subBody)
		}
	}))
	defer server.Close()

	p := New(providers.Options{AccessKeyID: "LTAItestkeyid", AccessKeySecret: "testsecret"})
	p.tokenBaseURL = server.URL
	p.gatewayBaseURL = server.URL
	p.now = func() time.Time { return now }

	bal, err := p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(bal.Sections) != 4 {
		t.Fatalf("Sections = %+v, want usage + addon + subscription", bal.Sections)
	}
	addon := section(t, bal.Sections, "addon-credits")
	if addon.UsedPct != 75 || addon.Remaining != 5000 || !addon.ResetAt.Equal(time.UnixMilli(1795564800000)) {
		t.Fatalf("addon = %+v", addon)
	}
	sub := section(t, bal.Sections, "subscription")
	if sub.UsedPct != -1 || sub.Remaining != 13 || !sub.ResetAt.Equal(time.UnixMilli(1794614400000)) {
		t.Fatalf("subscription = %+v", sub)
	}

	mu.Lock()
	if tokenCalls != 1 || calls[usageAPI] != 1 || calls[addonSummaryAPI] != 1 || calls[subscriptionAPI] != 1 {
		t.Fatalf("calls = token %d %v", tokenCalls, calls)
	}
	mu.Unlock()

	now = now.Add(10 * time.Minute)
	if _, err := p.Query(context.Background()); err != nil {
		t.Fatalf("Query within 12h: %v", err)
	}
	mu.Lock()
	if calls[usageAPI] != 2 || calls[addonSummaryAPI] != 1 || calls[subscriptionAPI] != 1 {
		t.Fatalf("calls within 12h = %v", calls)
	}
	mu.Unlock()

	now = now.Add(12 * time.Hour)
	if _, err := p.Query(context.Background()); err != nil {
		t.Fatalf("Query after 12h: %v", err)
	}
	mu.Lock()
	if calls[usageAPI] != 3 || calls[addonSummaryAPI] != 2 || calls[subscriptionAPI] != 2 {
		t.Fatalf("calls after 12h = %v", calls)
	}
	mu.Unlock()
}

func TestAddonNotAppendedWhenZeroOrExpired(t *testing.T) {
	usageBody := readFile(t, filepath.Join("testdata", "usage-data-v2.json"))
	addonBody := readFile(t, filepath.Join("testdata", "addon-summary.json"))
	zeroBody := readFile(t, filepath.Join("testdata", "addon-summary-zero.json"))

	cases := []struct {
		name    string
		now     time.Time
		addon   []byte
		wantAdd bool
	}{
		{"zero total", time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), zeroBody, false},
		{"expired", time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC), addonBody, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now := c.now
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tokenPath {
					fmt.Fprint(w, `{"Success":true,"cliAccessToken":"token-1"}`)
					return
				}
				api := testAPI(t, r)
				switch api {
				case usageAPI:
					w.Write(usageBody)
				case addonSummaryAPI:
					w.Write(c.addon)
				default:
					t.Fatalf("unexpected extra request: %s", api)
				}
			}))
			defer server.Close()

			p := New(providers.Options{AccessKeyID: "LTAItestkeyid", AccessKeySecret: "testsecret"})
			p.tokenBaseURL = server.URL
			p.gatewayBaseURL = server.URL
			p.now = func() time.Time { return now }
			p.lastSubscriptionAttempt = now
			bal, err := p.Query(context.Background())
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			for _, s := range bal.Sections {
				if s.Name == "addon-credits" {
					t.Fatalf("addon should not be appended: %+v", s)
				}
			}
		})
	}
}

func TestExtrasFailureDoesNotAffectQuery(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	usageBody := readFile(t, filepath.Join("testdata", "usage-data-v2.json"))

	var logBuf bytes.Buffer
	oldLogWriter := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(oldLogWriter) })

	var mu sync.Mutex
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == tokenPath {
			fmt.Fprint(w, `{"Success":true,"cliAccessToken":"token-1"}`)
			return
		}
		api := testAPI(t, r)
		mu.Lock()
		calls[api]++
		mu.Unlock()
		if api == usageAPI {
			w.Write(usageBody)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":"token-1 testsecret"}`)
	}))
	defer server.Close()

	p := New(providers.Options{AccessKeyID: "LTAItestkeyid", AccessKeySecret: "testsecret"})
	p.tokenBaseURL = server.URL
	p.gatewayBaseURL = server.URL
	p.now = func() time.Time { return now }
	bal, err := p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query should ignore extras failure: %v", err)
	}
	if len(bal.Sections) != 2 {
		t.Fatalf("Sections = %+v, want usage only", bal.Sections)
	}

	now = now.Add(time.Hour)
	if _, err := p.Query(context.Background()); err != nil {
		t.Fatalf("Query within 12h: %v", err)
	}
	mu.Lock()
	if calls[addonSummaryAPI] != 1 || calls[subscriptionAPI] != 1 {
		t.Fatalf("extras calls = %v, want 1 each before 12h", calls)
	}
	mu.Unlock()

	now = now.Add(11 * time.Hour)
	if _, err := p.Query(context.Background()); err != nil {
		t.Fatalf("Query after 12h: %v", err)
	}
	mu.Lock()
	if calls[addonSummaryAPI] != 2 || calls[subscriptionAPI] != 2 {
		t.Fatalf("extras calls = %v, want retry after 12h", calls)
	}
	mu.Unlock()
	logs := logBuf.String()
	for _, secret := range []string{"token-1", "testsecret"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs leak %q: %s", secret, logs)
		}
	}
}

func TestAddonNotLoginedRetriesWithNewToken(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	usageBody := readFile(t, filepath.Join("testdata", "usage-data-v2.json"))
	addonBody := readFile(t, filepath.Join("testdata", "addon-summary.json"))

	var mu sync.Mutex
	tokenCalls := 0
	addonCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == tokenPath {
			mu.Lock()
			tokenCalls++
			n := tokenCalls
			mu.Unlock()
			fmt.Fprintf(w, `{"Success":true,"cliAccessToken":"token-%d"}`, n)
			return
		}
		api := testAPI(t, r)
		switch api {
		case usageAPI:
			w.Write(usageBody)
		case addonSummaryAPI:
			mu.Lock()
			addonCalls++
			mu.Unlock()
			if r.Header.Get("Authorization") == "Bearer token-1" {
				fmt.Fprint(w, `{"data":{"success":false,"errorCode":"NotLogined"}}`)
				return
			}
			w.Write(addonBody)
		case subscriptionAPI:
			t.Fatal("subscription should be skipped")
		}
	}))
	defer server.Close()

	p := New(providers.Options{AccessKeyID: "LTAItestkeyid", AccessKeySecret: "testsecret"})
	p.tokenBaseURL = server.URL
	p.gatewayBaseURL = server.URL
	p.now = func() time.Time { return now }
	p.lastSubscriptionAttempt = now
	bal, err := p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	section(t, bal.Sections, "addon-credits")
	mu.Lock()
	defer mu.Unlock()
	if tokenCalls != 2 || addonCalls != 2 {
		t.Fatalf("token/addon calls = %d / %d, want 2 / 2", tokenCalls, addonCalls)
	}
}

func TestRemainingDays(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		end  time.Time
		want float64
	}{
		{time.Time{}, 0},
		{now.Add(-time.Second), 0},
		{now.Add(12 * time.Hour), 1},
		{now.Add(48 * time.Hour), 2},
		{now.Add(49 * time.Hour), 3},
	}
	for _, c := range cases {
		if got := remainingDays(now, c.end); got != c.want {
			t.Errorf("remainingDays(%v) = %v, want %v", c.end, got, c.want)
		}
	}
}
