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

const officialFeedURL = "https://willcodexresets.com/api/resets?limit=20"

const officialInterval = 15 * time.Minute

const officialTimeout = 5 * time.Second

const officialRetryDelay = time.Minute

type officialResponse struct {
	Resets []officialReset `json:"resets"`
}

type officialReset struct {
	Type           string    `json:"type"`
	AnnouncementAt time.Time `json:"announcementAtUtc"`
}

type officialLatest struct {
	global time.Time
	banked time.Time
}

func parseOfficial(data []byte) (officialLatest, error) {
	var resp officialResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return officialLatest{}, fmt.Errorf("解析官方重置记录失败: %w", err)
	}
	var out officialLatest
	for _, r := range resp.Resets {
		switch r.Type {
		case "global":
			if r.AnnouncementAt.After(out.global) {
				out.global = r.AnnouncementAt
			}
		case "banked":
			if r.AnnouncementAt.After(out.banked) {
				out.banked = r.AnnouncementAt
			}
		}
	}
	return out, nil
}

func (p *Provider) refreshOfficial(ctx context.Context, now time.Time) {
	if p.feedURL == "" {
		return
	}
	retry := !p.officialRetryAt.IsZero()
	switch {
	case retry && now.Before(p.officialRetryAt):
		return
	case retry:
		p.officialRetryAt = time.Time{}
	case !p.lastOfficialAttempt.IsZero() && now.Sub(p.lastOfficialAttempt) < officialInterval:
		return
	default:
		p.lastOfficialAttempt = now
	}
	latest, err := p.fetchOfficial(ctx)
	if err != nil {
		if retry {
			log.Printf("codex 官方重置记录重试失败，跳过本轮: %v", err)
			return
		}
		p.officialRetryAt = now.Add(officialRetryDelay)
		log.Printf("codex 官方重置记录查询失败，1 分钟后重试: %v", err)
		return
	}
	p.official = latest
	p.officialLoaded = true
}

func (p *Provider) fetchOfficial(ctx context.Context) (officialLatest, error) {
	reqCtx, cancel := context.WithTimeout(ctx, p.officialTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, p.feedURL, nil)
	if err != nil {
		return officialLatest{}, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "liquoten")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return officialLatest{}, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return officialLatest{}, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return officialLatest{}, fmt.Errorf("HTTP %s", resp.Status)
	}
	return parseOfficial(body)
}

func (p *Provider) officialSections() []providers.Section {
	if !p.officialLoaded {
		return nil
	}
	var out []providers.Section
	if !p.official.global.IsZero() {
		out = append(out, providers.Section{Name: "official-reset", UsedPct: -1, Remaining: -1, ResetAt: p.official.global})
	}
	if !p.official.banked.IsZero() {
		out = append(out, providers.Section{Name: "official-reset-card", UsedPct: -1, Remaining: -1, ResetAt: p.official.banked})
	}
	return out
}
