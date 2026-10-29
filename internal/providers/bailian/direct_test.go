package bailian

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func TestMain(m *testing.M) {
	bailianConfigPath = func() string { return "" }
	os.Exit(m.Run())
}

func TestSignACS3FixedVector(t *testing.T) {
	date := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	got := signACS3("LTAItestkeyid", "testsecret", http.MethodPost, "modelstudio.cn-beijing.aliyuncs.com",
		"/modelstudio/cli/generateAccessToken", "", "", date, "nonce-0001")
	want := "ACS3-HMAC-SHA256 Credential=LTAItestkeyid,SignedHeaders=content-type;host;x-acs-action;x-acs-content-sha256;x-acs-date;x-acs-signature-nonce;x-acs-version,Signature=d4b14ecdbce49d3f269be182400000286dd51d016db599cf5f812b806b94e621"
	if got != want {
		t.Fatalf("authorization = %q, want %q", got, want)
	}
}

func TestGatewayFor(t *testing.T) {
	cases := []struct {
		region, site string
		host, action string
	}{
		{"cn-beijing", "domestic", "bailian-cs.console.aliyun.com", "BroadScopeAspnGateway"},
		{"cn-beijing", "international", "bailian-cs.console.alibabacloud.com", "BroadScopeAspnGateway"},
		{"ap-southeast-1", "domestic", "modelstudio-cs.console.aliyun.com", "IntlBroadScopeAspnGateway"},
		{"ap-southeast-1", "international", "bailian-singapore-cs.alibabacloud.com", "IntlBroadScopeAspnGateway"},
		{"ap-southeast-2", "domestic", "bailian-cs.console.aliyun.com", "BroadScopeAspnGateway"},
		{"", "", "bailian-cs.console.aliyun.com", "BroadScopeAspnGateway"},
	}
	for _, c := range cases {
		host, action := gatewayFor(c.region, c.site)
		if host != c.host || action != c.action {
			t.Errorf("gatewayFor(%q, %q) = %q, %q; want %q, %q", c.region, c.site, host, action, c.host, c.action)
		}
	}
}

func TestTokenHost(t *testing.T) {
	if got := tokenHost("ap-southeast-1"); got != "modelstudio.ap-southeast-1.aliyuncs.com" {
		t.Fatalf("tokenHost(ap-southeast-1) = %q", got)
	}
	if got := tokenHost("cn-beijing"); got != "modelstudio.cn-beijing.aliyuncs.com" {
		t.Fatalf("tokenHost(cn-beijing) = %q", got)
	}
	if got := tokenHost("unknown"); got != "modelstudio.cn-beijing.aliyuncs.com" {
		t.Fatalf("tokenHost(unknown) = %q", got)
	}
}

func TestDirectQueryWithFixtures(t *testing.T) {
	cases := []struct {
		fixture string
		want    []providers.Section
	}{
		{
			"usage-data-v2.json",
			[]providers.Section{
				{Name: "5-hour quota", UsedPct: 42, Remaining: -1, ResetAt: time.UnixMilli(1768104000000)},
				{Name: "1-week quota", UsedPct: 18, Remaining: -1, ResetAt: time.UnixMilli(1768363200000)},
			},
		},
		{
			"usage-data.json",
			[]providers.Section{
				{Name: "5-hour quota", UsedPct: 85, Remaining: -1, ResetAt: time.UnixMilli(1768104000000)},
				{Name: "1-week quota", UsedPct: 91.5, Remaining: -1, ResetAt: time.UnixMilli(1768363200000)},
				{Name: "1-month quota", UsedPct: 33, Remaining: -1, ResetAt: time.UnixMilli(1791907200000)},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", c.fixture))
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			tokenCalls, usageCalls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case tokenPath:
					mu.Lock()
					tokenCalls++
					mu.Unlock()
					if r.Method != http.MethodPost {
						t.Errorf("token method = %s", r.Method)
					}
					if got := r.Header.Get("x-acs-action"); got != "GenerateCLIAccessToken" {
						t.Errorf("x-acs-action = %q", got)
					}
					if got := r.Header.Get("x-acs-version"); got != acsVersion {
						t.Errorf("x-acs-version = %q", got)
					}
					if got := r.Header.Get("content-type"); got != "application/json" {
						t.Errorf("content-type = %q", got)
					}
					if got := r.Header.Get("x-acs-content-sha256"); got != emptyBodySHA256() {
						t.Errorf("x-acs-content-sha256 = %q", got)
					}
					if got := r.Header.Get("x-acs-date"); got == "" {
						t.Error("x-acs-date is empty")
					}
					if got := r.Header.Get("x-acs-signature-nonce"); got == "" {
						t.Error("x-acs-signature-nonce is empty")
					}
					if got := r.Header.Get("authorization"); !strings.HasPrefix(got, "ACS3-HMAC-SHA256 Credential=LTAItestkeyid,") {
						t.Errorf("authorization = %q", got)
					}
					body, _ := io.ReadAll(r.Body)
					if len(body) != 0 {
						t.Errorf("token body = %q, want empty", body)
					}
					fmt.Fprint(w, `{"Success":true,"cliAccessToken":"token-1"}`)
				case "/cli/api.json":
					mu.Lock()
					usageCalls++
					mu.Unlock()
					if r.Method != http.MethodPost {
						t.Errorf("usage method = %s", r.Method)
					}
					if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
						t.Errorf("Authorization = %q", got)
					}
					if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
						t.Errorf("Content-Type = %q", got)
					}
					if got := r.Header.Get("Accept"); got != "*/*" {
						t.Errorf("Accept = %q", got)
					}
					if got := r.URL.Query().Get("action"); got != "BroadScopeAspnGateway" {
						t.Errorf("action = %q", got)
					}
					if got := r.URL.Query().Get("product"); got != "sfm_bailian" {
						t.Errorf("product = %q", got)
					}
					if got := r.URL.Query().Get("api"); got != usageAPI {
						t.Errorf("api = %q", got)
					}
					if err := r.ParseForm(); err != nil {
						t.Errorf("ParseForm: %v", err)
					}
					if got := r.Form.Get("region"); got != "cn-beijing" {
						t.Errorf("region = %q", got)
					}
					var params struct {
						API  string `json:"Api"`
						V    string `json:"V"`
						Data struct {
							CornerstoneParam struct {
								Protocol       string `json:"protocol"`
								Console        string `json:"console"`
								ProductCode    string `json:"productCode"`
								SwitchUserType int    `json:"switchUserType"`
								ConsoleSite    string `json:"consoleSite"`
								SwitchAgent    *bool  `json:"switchAgent"`
							} `json:"cornerstoneParam"`
						} `json:"Data"`
					}
					if err := json.Unmarshal([]byte(r.Form.Get("params")), &params); err != nil {
						t.Errorf("params JSON: %v", err)
					}
					cp := params.Data.CornerstoneParam
					if params.API != usageAPI || params.V != "1.0" || cp.Protocol != "V2" ||
						cp.Console != "ONE_CONSOLE" || cp.ProductCode != "p_efm" ||
						cp.SwitchUserType != 3 || cp.ConsoleSite != "BAILIAN_ALIYUN" {
						t.Errorf("params = %+v", params)
					}
					if cp.SwitchAgent == nil || !*cp.SwitchAgent {
						t.Errorf("switchAgent = %v, want true", cp.SwitchAgent)
					}
					w.Header().Set("Content-Type", "application/json")
					w.Write(raw)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			p := New(providers.Options{AccessKeyID: "LTAItestkeyid", AccessKeySecret: "testsecret"})
			p.tokenBaseURL = server.URL
			p.gatewayBaseURL = server.URL
			p.switchAgent = json.RawMessage("true")
			bal, err := p.Query(context.Background())
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if tokenCalls != 1 || usageCalls != 1 {
				t.Fatalf("calls = token %d, usage %d; want 1, 1", tokenCalls, usageCalls)
			}
			if len(bal.Sections) != len(c.want) {
				t.Fatalf("Sections = %+v, want %+v", bal.Sections, c.want)
			}
			for i := range c.want {
				got, want := bal.Sections[i], c.want[i]
				if got.Name != want.Name || got.UsedPct != want.UsedPct || got.Remaining != want.Remaining || !got.ResetAt.Equal(want.ResetAt) {
					t.Errorf("Sections[%d] = %+v, want %+v", i, got, want)
				}
			}
		})
	}
}

func TestNotLoginedRetry(t *testing.T) {
	var mu sync.Mutex
	tokenCalls, usageCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tokenPath:
			mu.Lock()
			tokenCalls++
			n := tokenCalls
			mu.Unlock()
			fmt.Fprintf(w, `{"Success":true,"cliAccessToken":"token-%d"}`, n)
		case "/cli/api.json":
			mu.Lock()
			usageCalls++
			mu.Unlock()
			if r.Header.Get("Authorization") == "Bearer token-1" {
				fmt.Fprint(w, `{"data":{"success":false,"errorCode":"NotLogined"}}`)
				return
			}
			raw, err := os.ReadFile(filepath.Join("testdata", "usage-data.json"))
			if err != nil {
				t.Errorf("read fixture: %v", err)
				return
			}
			w.Write(raw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p := New(providers.Options{AccessKeyID: "LTAItestkeyid", AccessKeySecret: "testsecret"})
	p.tokenBaseURL = server.URL
	p.gatewayBaseURL = server.URL
	bal, err := p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(bal.Sections) != 3 {
		t.Fatalf("Sections = %+v, want 3", bal.Sections)
	}
	if tokenCalls != 2 || usageCalls != 2 {
		t.Fatalf("calls = token %d, usage %d; want 2, 2", tokenCalls, usageCalls)
	}
}

func TestNotLoginedRetriesOnlyOnce(t *testing.T) {
	var mu sync.Mutex
	tokenCalls, usageCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tokenPath:
			mu.Lock()
			tokenCalls++
			n := tokenCalls
			mu.Unlock()
			fmt.Fprintf(w, `{"Success":true,"cliAccessToken":"token-%d"}`, n)
		case "/cli/api.json":
			mu.Lock()
			usageCalls++
			mu.Unlock()
			fmt.Fprint(w, `{"data":{"success":false,"errorCode":"NotLogined"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p := New(providers.Options{AccessKeyID: "LTAItestkeyid", AccessKeySecret: "testsecret"})
	p.tokenBaseURL = server.URL
	p.gatewayBaseURL = server.URL
	_, err := p.Query(context.Background())
	if err == nil {
		t.Fatal("Query should fail when retry is still NotLogined")
	}
	if !strings.Contains(err.Error(), "查用量") {
		t.Fatalf("error = %q, want 查用量 stage", err)
	}
	if tokenCalls != 2 || usageCalls != 2 {
		t.Fatalf("calls = token %d, usage %d; want 2, 2", tokenCalls, usageCalls)
	}
}

func TestTokenCachedAcrossQueries(t *testing.T) {
	var mu sync.Mutex
	tokenCalls, usageCalls := 0, 0
	raw, err := os.ReadFile(filepath.Join("testdata", "usage-data-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tokenPath:
			mu.Lock()
			tokenCalls++
			mu.Unlock()
			fmt.Fprint(w, `{"Success":true,"cliAccessToken":"token-1"}`)
		case "/cli/api.json":
			mu.Lock()
			usageCalls++
			mu.Unlock()
			w.Write(raw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p := New(providers.Options{AccessKeyID: "LTAItestkeyid", AccessKeySecret: "testsecret"})
	p.tokenBaseURL = server.URL
	p.gatewayBaseURL = server.URL
	for i := 0; i < 2; i++ {
		if _, err := p.Query(context.Background()); err != nil {
			t.Fatalf("Query #%d: %v", i+1, err)
		}
	}
	if tokenCalls != 1 || usageCalls != 2 {
		t.Fatalf("calls = token %d, usage %d; want 1, 2", tokenCalls, usageCalls)
	}
}

func TestTokenFailureDoesNotLeakSecret(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"success false", `{"Success":false,"Code":"InvalidAccessKeyId","Message":"bad credential"}`},
		{"empty token", `{"Success":true,"cliAccessToken":""}`},
		{"http error echoes secret", `{"Code":"Forbidden","Message":"testsecret"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.name == "http error echoes secret" {
					w.WriteHeader(http.StatusForbidden)
				}
				fmt.Fprint(w, c.body)
			}))
			defer server.Close()

			p := New(providers.Options{AccessKeyID: "LTAItestkeyid", AccessKeySecret: "testsecret"})
			p.tokenBaseURL = server.URL
			p.gatewayBaseURL = server.URL
			_, err := p.Query(context.Background())
			if err == nil {
				t.Fatal("Query should fail")
			}
			if !strings.Contains(err.Error(), "换 token 失败") {
				t.Fatalf("error = %q, want 换 token stage", err)
			}
			if strings.Contains(err.Error(), "testsecret") {
				t.Fatalf("error leaks secret: %q", err)
			}
		})
	}
}

func TestBLConfigCredentialsAndRegion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{
  "access_key_id": "LTAIfrombl",
  "access_key_secret": "bl-secret",
  "console_region": "ap-southeast-1",
  "console_site": "international",
  "console_switch_agent": true
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := bailianConfigPath
	t.Cleanup(func() { bailianConfigPath = orig })
	bailianConfigPath = func() string { return path }

	p := New(providers.Options{})
	if p.accessKeyID != "LTAIfrombl" || p.accessKeySecret != "bl-secret" {
		t.Fatalf("credentials = %q / %q", p.accessKeyID, p.accessKeySecret)
	}
	if p.region != "ap-southeast-1" || p.site != "international" {
		t.Fatalf("region/site = %q / %q", p.region, p.site)
	}
	if string(p.switchAgent) != "true" {
		t.Fatalf("switchAgent = %s", p.switchAgent)
	}
	if !p.hasCredentials() {
		t.Fatal("hasCredentials should be true")
	}
	if got, err := p.Preflight(context.Background()); err != nil || got != "bl" {
		t.Fatalf("Preflight = %q, %v; want command unchanged", got, err)
	}

	q := New(providers.Options{AccessKeyID: "LTAIconfig", AccessKeySecret: "config-secret"})
	if q.accessKeyID != "LTAIconfig" || q.accessKeySecret != "config-secret" {
		t.Fatalf("config credentials should win: %q / %q", q.accessKeyID, q.accessKeySecret)
	}
	if q.region != "ap-southeast-1" || q.site != "international" {
		t.Fatalf("config mode should keep startup region/site: %q / %q", q.region, q.site)
	}
}

func TestBLConfigReloadSwitchesToDirectAndInvalidatesToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	orig := bailianConfigPath
	t.Cleanup(func() { bailianConfigPath = orig })
	bailianConfigPath = func() string { return path }

	fake, err := filepath.Abs(filepath.Join("testdata", "fake-bl.sh"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "usage-data-v2.json"))
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	tokenCalls, usageCalls := 0, 0
	lastAuthorization := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tokenPath:
			mu.Lock()
			tokenCalls++
			n := tokenCalls
			lastAuthorization = r.Header.Get("authorization")
			mu.Unlock()
			fmt.Fprintf(w, `{"Success":true,"cliAccessToken":"token-%d"}`, n)
		case "/cli/api.json":
			mu.Lock()
			usageCalls++
			mu.Unlock()
			w.Write(raw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	p := New(providers.Options{Command: fake})
	p.tokenBaseURL = server.URL
	p.gatewayBaseURL = server.URL

	bal, err := p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query via bl: %v", err)
	}
	if len(bal.Sections) == 0 || bal.Sections[0].UsedPct != 37.4 {
		t.Fatalf("initial Sections = %+v, want bl fixture 37.4%%", bal.Sections)
	}
	mu.Lock()
	if tokenCalls != 0 || usageCalls != 0 {
		t.Fatalf("initial direct calls = token %d, usage %d; want 0, 0", tokenCalls, usageCalls)
	}
	mu.Unlock()

	writeConfig := func(id, region string) {
		content := fmt.Sprintf(`{"access_key_id":%q,"access_key_secret":"bl-secret","console_region":%q,"console_site":"domestic"}`, id, region)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig("LTAI-1", "cn-beijing")
	bal, err = p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query via direct: %v", err)
	}
	if len(bal.Sections) == 0 || bal.Sections[0].UsedPct != 42 {
		t.Fatalf("direct Sections = %+v, want 42%%", bal.Sections)
	}
	mu.Lock()
	if tokenCalls != 1 || usageCalls != 1 {
		t.Fatalf("direct calls = token %d, usage %d; want 1, 1", tokenCalls, usageCalls)
	}
	if !strings.Contains(lastAuthorization, "Credential=LTAI-1,") {
		t.Fatalf("authorization = %q, want Credential=LTAI-1", lastAuthorization)
	}
	mu.Unlock()

	writeConfig("LTAI-2", "cn-beijing")
	if _, err := p.Query(context.Background()); err != nil {
		t.Fatalf("Query after AK ID change: %v", err)
	}
	mu.Lock()
	if tokenCalls != 2 || usageCalls != 2 {
		t.Fatalf("calls after AK ID change = token %d, usage %d; want 2, 2", tokenCalls, usageCalls)
	}
	if !strings.Contains(lastAuthorization, "Credential=LTAI-2,") {
		t.Fatalf("authorization after AK ID change = %q, want Credential=LTAI-2", lastAuthorization)
	}
	mu.Unlock()

	writeConfig("LTAI-2", "ap-southeast-1")
	if _, err := p.Query(context.Background()); err != nil {
		t.Fatalf("Query after region change: %v", err)
	}
	mu.Lock()
	if tokenCalls != 3 || usageCalls != 3 {
		t.Fatalf("calls after region change = token %d, usage %d; want 3, 3", tokenCalls, usageCalls)
	}
	mu.Unlock()

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	bal, err = p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query after config removal: %v", err)
	}
	if len(bal.Sections) == 0 || bal.Sections[0].UsedPct != 37.4 {
		t.Fatalf("Sections after config removal = %+v, want bl fixture 37.4%%", bal.Sections)
	}
	mu.Lock()
	if tokenCalls != 3 || usageCalls != 3 {
		t.Fatalf("calls after config removal = token %d, usage %d; want 3, 3", tokenCalls, usageCalls)
	}
	mu.Unlock()

	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	bal, err = p.Query(context.Background())
	if err != nil {
		t.Fatalf("Query with invalid config: %v", err)
	}
	if len(bal.Sections) == 0 || bal.Sections[0].UsedPct != 37.4 {
		t.Fatalf("Sections with invalid config = %+v, want bl fixture 37.4%%", bal.Sections)
	}
}

func TestPreflightReloadsBLConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	orig := bailianConfigPath
	t.Cleanup(func() { bailianConfigPath = orig })
	bailianConfigPath = func() string { return path }
	origDirs := searchDirs
	t.Cleanup(func() { searchDirs = origDirs })
	searchDirs = func() []string { return nil }
	t.Setenv("PATH", t.TempDir())

	p := New(providers.Options{Command: "/definitely/not/a/real/bl"})
	if _, err := p.Preflight(context.Background()); err == nil {
		t.Fatal("Preflight should fail without bl or AK/SK")
	}
	content := `{"access_key_id":"LTAI-1","access_key_secret":"bl-secret"}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := p.Preflight(context.Background())
	if err != nil {
		t.Fatalf("Preflight after config: %v", err)
	}
	if got != p.command {
		t.Fatalf("Preflight = %q, want %q", got, p.command)
	}
}

func TestPreflightDirectWithoutBL(t *testing.T) {
	p := New(providers.Options{
		AccessKeyID:     "LTAItestkeyid",
		AccessKeySecret: "testsecret",
		Command:         "/definitely/not/a/real/bl",
	})
	got, err := p.Preflight(context.Background())
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if got != p.command {
		t.Fatalf("Preflight = %q, want %q", got, p.command)
	}
}

func TestParseUsageResponseFallbacks(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"DataV2.data", `{"data":{"success":true,"DataV2":{"data":{"per5HourPercentage":0.5}}}}`},
		{"DataV2", `{"data":{"success":true,"DataV2":{"per5HourPercentage":0.5}}}`},
		{"data only", `{"data":{"success":true,"per5HourPercentage":0.5}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := parseUsageResponse([]byte(c.raw))
			if err != nil {
				t.Fatalf("parseUsageResponse: %v", err)
			}
			tp, err := parseTokenPlan(raw)
			if err != nil {
				t.Fatalf("parseTokenPlan: %v", err)
			}
			if tp.Per5Hour == nil || *tp.Per5Hour != 0.5 {
				t.Fatalf("Per5Hour = %v, want 0.5", tp.Per5Hour)
			}
		})
	}
}
