package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

const tiboFeedURL = "https://willcodexresets.com/api/resets?limit=20"

const tiboInterval = 15 * time.Minute

const tiboTimeout = 5 * time.Second

const tiboRetryDelay = time.Minute

type tiboResponse struct {
	Resets []tiboReset `json:"resets"`
}

type tiboReset struct {
	Type           string    `json:"type"`
	AnnouncementAt time.Time `json:"announcementAtUtc"`
}

func parseTibo(data []byte) (time.Time, error) {
	var resp tiboResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return time.Time{}, fmt.Errorf("解析 Tibo 重置记录失败: %w", err)
	}
	var latest time.Time
	for _, r := range resp.Resets {
		if r.Type == "global" && r.AnnouncementAt.After(latest) {
			latest = r.AnnouncementAt
		}
	}
	return latest, nil
}

func (p *Provider) refreshTibo(ctx context.Context, now time.Time) {
	if p.feedURL == "" {
		return
	}
	retry := !p.tiboRetryAt.IsZero()
	switch {
	case retry && now.Before(p.tiboRetryAt):
		return
	case retry:
		p.tiboRetryAt = time.Time{}
	case !p.lastTiboAttempt.IsZero() && now.Sub(p.lastTiboAttempt) < tiboInterval:
		return
	default:
		p.lastTiboAttempt = now
	}
	latest, err := p.fetchTibo(ctx)
	if err != nil {
		if retry {
			log.Printf("codex Tibo 重置记录重试失败，跳过本轮: %v", err)
			return
		}
		p.tiboRetryAt = now.Add(tiboRetryDelay)
		log.Printf("codex Tibo 重置记录查询失败，1 分钟后重试: %v", err)
		return
	}
	p.tibo = latest
	p.tiboLoaded = true
}

func (p *Provider) fetchTibo(ctx context.Context) (time.Time, error) {
	reqCtx, cancel := context.WithTimeout(ctx, p.tiboTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, p.feedURL, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "liquoten")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return time.Time{}, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return time.Time{}, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return time.Time{}, fmt.Errorf("HTTP %s", resp.Status)
	}
	return parseTibo(body)
}

func (p *Provider) tiboSections() []providers.Section {
	if !p.tiboLoaded || p.tibo.IsZero() {
		return nil
	}
	return []providers.Section{{Name: "tibo-reset", UsedPct: -1, Remaining: -1, ResetAt: p.tibo}}
}
