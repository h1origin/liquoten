package qoder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lk/liquoten-cred/internal/providers"
)

type fakeServer struct {
	exchanges atomic.Int32
	usageHits atomic.Int32
	reject401 atomic.Int32
	usage     []byte
}

func (f *fakeServer) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/jobToken/exchange", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["personal_token"] != "pt-good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		n := f.exchanges.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"token": "job-" + string(rune('0'+n)), "expires_in": 3600})
	})
	mux.HandleFunc("GET /api/v2/quota/usage", func(w http.ResponseWriter, r *http.Request) {
		f.usageHits.Add(1)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer job-") || f.reject401.Add(-1) >= 0 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write(f.usage)
	})
	return mux
}

func newTest(t *testing.T, pat string) (*Provider, *fakeServer) {
	t.Helper()
	usage, err := os.ReadFile(filepath.Join("testdata", "usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{usage: usage}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return New(Site{Name: CN.Name, OpenAPIURL: srv.URL}, providers.Options{APIKey: pat}), f
}

func TestQuery(t *testing.T) {
	p, f := newTest(t, " pt-good\n")
	bal, err := p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if bal.Provider != "qodercn" || len(bal.Sections) != 2 {
		t.Fatalf("bal = %+v", bal)
	}
	m := bal.Sections[0]
	if m.Name != "1-month quota" || m.UsedPct != 25 || m.Remaining != 1500 || !m.ResetAt.Equal(time.UnixMilli(1793462400000)) {
		t.Fatalf("month = %+v", m)
	}
	c := bal.Sections[1]
	if c.Name != "credits" || c.UsedPct != -1 || c.Remaining != 800 {
		t.Fatalf("add-on = %+v", c)
	}

	if _, err := p.Query(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.exchanges.Load(); n != 1 {
		t.Fatalf("job token 应缓存复用，exchange 次数 = %d", n)
	}
}

func TestQueryReexchangesOn401(t *testing.T) {
	p, f := newTest(t, "pt-good")
	f.reject401.Store(1)
	if _, err := p.Query(context.Background()); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if f.exchanges.Load() != 2 || f.usageHits.Load() != 2 {
		t.Fatalf("401 后应重新换 token 并重试: exchanges=%d hits=%d", f.exchanges.Load(), f.usageHits.Load())
	}
}

func TestQueryBadKey(t *testing.T) {
	p, _ := newTest(t, "pt-bad")
	_, err := p.Query(context.Background())
	if err == nil || !strings.Contains(err.Error(), "API Key 无效") {
		t.Fatalf("err = %v, want API Key 无效", err)
	}
	empty, _ := newTest(t, "")
	if _, err := empty.Query(context.Background()); err == nil {
		t.Fatal("未设置 key 时应报错")
	}
}

func TestParseUsageTeams(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "usage_teams.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := parseUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 {
		t.Fatalf("sections = %+v", s)
	}
	m := s[0]
	want := 53848.0 / 171497 * 100
	if m.Name != "1-month quota" || m.UsedPct != want || m.Remaining != 117649 || !m.ResetAt.Equal(time.UnixMilli(1792684800000)) {
		t.Fatalf("team = %+v", m)
	}
}

func TestParseUsageVariants(t *testing.T) {
	raw := []byte(`{"data":{"user_quota":{"cap":100,"percentage":0.4},"expires_at":"9999-12-31T00:00:00Z"}}`)
	s, err := parseUsage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 || s[0].UsedPct != 40 || s[0].Remaining != -1 || !s[0].ResetAt.IsZero() {
		t.Fatalf("sections = %+v", s)
	}
	if _, err := parseUsage([]byte(`{"userQuota":{"total":0}}`)); err == nil {
		t.Fatal("没有额度数据时应报错")
	}
}

func TestRegistered(t *testing.T) {
	for _, name := range []string{"qoder", "qodercn"} {
		p, err := providers.New(name, providers.Options{APIKey: "x"})
		if err != nil || p.Name() != name {
			t.Fatalf("%s: %v %v", name, p, err)
		}
		if k, ok := p.(providers.KeyRequired); !ok || !k.RequiresAPIKey() {
			t.Fatalf("%s 应要求 API Key", name)
		}
	}
}
