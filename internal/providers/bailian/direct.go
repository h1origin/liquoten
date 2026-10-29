package bailian

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

const (
	defaultRegion = "cn-beijing"
	defaultSite   = "domestic"
	acsVersion    = "2026-02-10"
	tokenPath     = "/modelstudio/cli/generateAccessToken"
	usageAPI      = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage"
)

var (
	errTokenExpired = errors.New("token 失效")

	bailianConfigPath = func() string {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, ".bailian", "config.json")
	}
)

type bailianConfig struct {
	AccessKeyID        string          `json:"access_key_id"`
	AccessKeySecret    string          `json:"access_key_secret"`
	ConsoleRegion      string          `json:"console_region"`
	ConsoleSite        string          `json:"console_site"`
	ConsoleSwitchAgent json.RawMessage `json:"console_switch_agent"`
}

type cliTokenResponse struct {
	Success        *bool  `json:"success"`
	Code           string `json:"code"`
	Message        string `json:"message"`
	CLIAccessToken string `json:"cliAccessToken"`
}

type usagePayload struct {
	API  string `json:"Api"`
	V    string `json:"V"`
	Data struct {
		CornerstoneParam struct {
			Protocol       string          `json:"protocol"`
			Console        string          `json:"console"`
			ProductCode    string          `json:"productCode"`
			SwitchUserType int             `json:"switchUserType"`
			ConsoleSite    string          `json:"consoleSite"`
			SwitchAgent    json.RawMessage `json:"switchAgent,omitempty"`
		} `json:"cornerstoneParam"`
	} `json:"Data"`
}

type authSnapshot struct {
	accessKeyID     string
	accessKeySecret string
	region          string
	site            string
	switchAgent     json.RawMessage
}

func (a authSnapshot) hasCredentials() bool {
	return a.accessKeyID != "" && a.accessKeySecret != ""
}

func (a authSnapshot) sameTokenScope(b authSnapshot) bool {
	return a.accessKeyID == b.accessKeyID && a.accessKeySecret == b.accessKeySecret &&
		a.region == b.region && a.site == b.site
}

func (p *Provider) hasConfiguredCredentials() bool {
	return p.configuredAccessKeyID != "" && p.configuredAccessKeySecret != ""
}

func (p *Provider) hasCredentials() bool {
	return p.snapshot().hasCredentials()
}

func (p *Provider) snapshot() authSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

func (p *Provider) snapshotLocked() authSnapshot {
	return authSnapshot{
		accessKeyID:     p.accessKeyID,
		accessKeySecret: p.accessKeySecret,
		region:          p.region,
		site:            p.site,
		switchAgent:     p.switchAgent,
	}
}

func (p *Provider) initAuth() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hasConfiguredCredentials() {
		p.accessKeyID = p.configuredAccessKeyID
		p.accessKeySecret = p.configuredAccessKeySecret
		if cfg, err := readBLConfig(); err == nil {
			p.region = normalizeRegion(cfg.ConsoleRegion)
			p.site = normalizeSite(cfg.ConsoleSite)
			p.switchAgent = switchAgentFromConfig(cfg.ConsoleSwitchAgent)
		}
		return
	}
	p.refreshAuthLocked()
}

func (p *Provider) refreshAuth() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshAuthLocked()
}

func (p *Provider) refreshAuthLocked() {
	if p.hasConfiguredCredentials() {
		return
	}
	next := authSnapshot{region: defaultRegion, site: defaultSite}
	if cfg, err := readBLConfig(); err == nil {
		next.accessKeyID = cfg.AccessKeyID
		next.accessKeySecret = cfg.AccessKeySecret
		next.region = normalizeRegion(cfg.ConsoleRegion)
		next.site = normalizeSite(cfg.ConsoleSite)
		next.switchAgent = switchAgentFromConfig(cfg.ConsoleSwitchAgent)
	}
	if p.configuredAccessKeyID != "" {
		next.accessKeyID = p.configuredAccessKeyID
	}
	if p.configuredAccessKeySecret != "" {
		next.accessKeySecret = p.configuredAccessKeySecret
	}
	if !p.snapshotLocked().sameTokenScope(next) {
		p.token = ""
	}
	p.accessKeyID = next.accessKeyID
	p.accessKeySecret = next.accessKeySecret
	p.region = next.region
	p.site = next.site
	p.switchAgent = next.switchAgent
}

func readBLConfig() (bailianConfig, error) {
	path := bailianConfigPath()
	if path == "" {
		return bailianConfig{}, errors.New("未配置 bl 配置文件路径")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return bailianConfig{}, err
	}
	var cfg bailianConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return bailianConfig{}, err
	}
	return cfg, nil
}

func switchAgentFromConfig(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

func normalizeRegion(region string) string {
	if region == "ap-southeast-1" {
		return region
	}
	return defaultRegion
}

func normalizeSite(site string) string {
	if site == "international" {
		return site
	}
	return defaultSite
}

func tokenHost(region string) string {
	if normalizeRegion(region) == "ap-southeast-1" {
		return "modelstudio.ap-southeast-1.aliyuncs.com"
	}
	return "modelstudio.cn-beijing.aliyuncs.com"
}

func gatewayFor(region, site string) (string, string) {
	region = normalizeRegion(region)
	site = normalizeSite(site)
	switch region {
	case "ap-southeast-1":
		if site == "international" {
			return "bailian-singapore-cs.alibabacloud.com", "IntlBroadScopeAspnGateway"
		}
		return "modelstudio-cs.console.aliyun.com", "IntlBroadScopeAspnGateway"
	default:
		if site == "international" {
			return "bailian-cs.console.alibabacloud.com", "BroadScopeAspnGateway"
		}
		return "bailian-cs.console.aliyun.com", "BroadScopeAspnGateway"
	}
}

func signACS3(akID, akSecret, method, host, path, query, body string, date time.Time, nonce string) string {
	bodySum := sha256.Sum256([]byte(body))
	bodyHash := hex.EncodeToString(bodySum[:])
	headers := map[string]string{
		"content-type":          "application/json",
		"host":                  host,
		"x-acs-action":          "GenerateCLIAccessToken",
		"x-acs-content-sha256":  bodyHash,
		"x-acs-date":            date.UTC().Format("2006-01-02T15:04:05Z"),
		"x-acs-signature-nonce": nonce,
		"x-acs-version":         acsVersion,
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(headers[name])
		canonicalHeaders.WriteString("\n")
	}
	signedHeaders := strings.Join(names, ";")
	canonicalRequest := strings.Join([]string{
		method,
		path,
		query,
		canonicalHeaders.String(),
		signedHeaders,
		bodyHash,
	}, "\n")
	requestSum := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "ACS3-HMAC-SHA256\n" + hex.EncodeToString(requestSum[:])
	mac := hmac.New(sha256.New, []byte(akSecret))
	mac.Write([]byte(stringToSign))
	signature := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("ACS3-HMAC-SHA256 Credential=%s,SignedHeaders=%s,Signature=%s", akID, signedHeaders, signature)
}

func randomNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (p *Provider) tokenEndpoint(auth authSnapshot) string {
	base := p.tokenBaseURL
	if base == "" {
		base = "https://" + tokenHost(auth.region)
	}
	return strings.TrimRight(base, "/") + tokenPath
}

func (p *Provider) generateToken(ctx context.Context, auth authSnapshot) (string, error) {
	endpoint := p.tokenEndpoint(auth)
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("换 token 失败: 无效的地址: %w", err)
	}
	nonce, err := randomNonce()
	if err != nil {
		return "", fmt.Errorf("换 token 失败: 生成随机串失败: %w", err)
	}
	date := time.Now().UTC()
	authorization := signACS3(auth.accessKeyID, auth.accessKeySecret, http.MethodPost, u.Host, u.Path, u.RawQuery, "", date, nonce)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(""))
	if err != nil {
		return "", fmt.Errorf("换 token 失败: 构造请求失败: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-acs-action", "GenerateCLIAccessToken")
	req.Header.Set("x-acs-version", acsVersion)
	req.Header.Set("x-acs-date", date.Format("2006-01-02T15:04:05Z"))
	req.Header.Set("x-acs-signature-nonce", nonce)
	req.Header.Set("x-acs-content-sha256", emptyBodySHA256())
	req.Header.Set("authorization", authorization)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("换 token 失败: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("换 token 失败: 读取响应失败: %w", err)
	}

	var tr cliTokenResponse
	jsonErr := json.Unmarshal(body, &tr)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := redactValue(p.redact(apiMessage(tr.Code, tr.Message)), auth.accessKeySecret)
		if msg == "" {
			msg = redactValue(p.redact(truncate(string(body), 500)), auth.accessKeySecret)
		}
		return "", fmt.Errorf("换 token 失败: HTTP %s: %s", resp.Status, msg)
	}
	if jsonErr != nil {
		return "", fmt.Errorf("换 token 失败: 解析响应失败: %w", jsonErr)
	}
	if tr.Success != nil && !*tr.Success {
		msg := redactValue(p.redact(apiMessage(tr.Code, tr.Message)), auth.accessKeySecret)
		if msg == "" {
			msg = "请求被拒绝"
		}
		return "", fmt.Errorf("换 token 失败: %s", msg)
	}
	if tr.CLIAccessToken == "" {
		msg := redactValue(p.redact(apiMessage(tr.Code, tr.Message)), auth.accessKeySecret)
		if msg == "" {
			msg = "响应中没有 cliAccessToken"
		}
		return "", fmt.Errorf("换 token 失败: %s", msg)
	}
	return tr.CLIAccessToken, nil
}

func emptyBodySHA256() string {
	sum := sha256.Sum256(nil)
	return hex.EncodeToString(sum[:])
}

func apiMessage(code, message string) string {
	switch {
	case code != "" && message != "":
		return code + ": " + message
	case code != "":
		return code
	default:
		return message
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func redactValue(s, value string) string {
	if value == "" {
		return s
	}
	return strings.ReplaceAll(s, value, "[REDACTED]")
}

func (p *Provider) redact(s string) string {
	p.mu.Lock()
	secret, token := p.accessKeySecret, p.token
	p.mu.Unlock()
	return redactValue(redactValue(s, secret), token)
}

func (p *Provider) ensureToken(ctx context.Context, auth authSnapshot) (string, error) {
	p.mu.Lock()
	token := p.token
	current := p.snapshotLocked()
	p.mu.Unlock()
	if token != "" && current.sameTokenScope(auth) {
		return token, nil
	}
	token, err := p.generateToken(ctx, auth)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	if p.snapshotLocked().sameTokenScope(auth) {
		if p.token == "" {
			p.token = token
		} else {
			token = p.token
		}
	}
	p.mu.Unlock()
	return token, nil
}

func (p *Provider) clearToken(token string) {
	p.mu.Lock()
	if p.token == token {
		p.token = ""
	}
	p.mu.Unlock()
}

func (p *Provider) usageParams(auth authSnapshot) ([]byte, error) {
	payload := usagePayload{API: usageAPI, V: "1.0"}
	payload.Data.CornerstoneParam.Protocol = "V2"
	payload.Data.CornerstoneParam.Console = "ONE_CONSOLE"
	payload.Data.CornerstoneParam.ProductCode = "p_efm"
	payload.Data.CornerstoneParam.SwitchUserType = 3
	payload.Data.CornerstoneParam.ConsoleSite = "BAILIAN_ALIYUN"
	payload.Data.CornerstoneParam.SwitchAgent = auth.switchAgent
	return json.Marshal(payload)
}

func (p *Provider) usageEndpoint(auth authSnapshot) string {
	host, action := gatewayFor(auth.region, auth.site)
	base := p.gatewayBaseURL
	if base == "" {
		base = "https://" + host
	}
	return strings.TrimRight(base, "/") + "/cli/api.json?action=" + url.QueryEscape(action) +
		"&product=sfm_bailian&api=" + url.QueryEscape(usageAPI)
}

func (p *Provider) fetchUsage(ctx context.Context, auth authSnapshot, token string) ([]byte, error) {
	params, err := p.usageParams(auth)
	if err != nil {
		return nil, fmt.Errorf("构造请求参数失败: %w", err)
	}
	endpoint := p.usageEndpoint(auth)
	form := url.Values{}
	form.Set("region", auth.region)
	form.Set("params", string(params))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "*/*")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s: %s", resp.Status, redactValue(p.redact(truncate(string(body), 500)), auth.accessKeySecret))
	}
	return parseUsageResponse(body)
}

func parseUsageResponse(raw []byte) ([]byte, error) {
	var top struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if len(top.Data) == 0 {
		return nil, errors.New("响应缺少 data")
	}
	var data struct {
		Success   *bool           `json:"success"`
		ErrorCode string          `json:"errorCode"`
		DataV2    json.RawMessage `json:"DataV2"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(top.Data, &data); err != nil {
		return nil, fmt.Errorf("解析响应 data 失败: %w", err)
	}
	if data.Success != nil && !*data.Success {
		if strings.Contains(data.ErrorCode, "NotLogined") {
			return nil, fmt.Errorf("%w: %s", errTokenExpired, data.ErrorCode)
		}
		return nil, fmt.Errorf("用量查询返回失败: %s", apiMessage(data.ErrorCode, "未知错误"))
	}
	if presentJSON(data.DataV2) {
		return unwrapDataV2(data.DataV2)
	}
	if presentJSON(data.Data) {
		return data.Data, nil
	}
	return top.Data, nil
}

func unwrapDataV2(raw json.RawMessage) ([]byte, error) {
	var dv struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &dv); err != nil {
		return nil, fmt.Errorf("解析 DataV2 失败: %w", err)
	}
	if !presentJSON(dv.Data) {
		return raw, nil
	}
	var dd struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(dv.Data, &dd); err == nil && presentJSON(dd.Data) {
		return dd.Data, nil
	}
	return dv.Data, nil
}

func presentJSON(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

func (p *Provider) queryDirect(ctx context.Context, bal providers.Balance, auth authSnapshot) (providers.Balance, error) {
	token, err := p.ensureToken(ctx, auth)
	if err != nil {
		return bal, err
	}
	raw, err := p.fetchUsage(ctx, auth, token)
	if errors.Is(err, errTokenExpired) {
		p.clearToken(token)
		token, err = p.ensureToken(ctx, auth)
		if err != nil {
			return bal, fmt.Errorf("查用量时 token 失效，重新换 token 失败: %w", err)
		}
		raw, err = p.fetchUsage(ctx, auth, token)
		if err != nil {
			return bal, fmt.Errorf("查用量失败（已重试一次）: %w", err)
		}
	} else if err != nil {
		return bal, fmt.Errorf("查用量失败: %w", err)
	}

	tp, err := parseTokenPlan(raw)
	if err != nil {
		return bal, fmt.Errorf("解析 Token Plan 用量失败: %w", err)
	}
	bal.Sections = sections(tp)
	if len(bal.Sections) == 0 {
		return bal, errors.New("用量响应中没有可识别的额度字段")
	}
	return bal, nil
}
