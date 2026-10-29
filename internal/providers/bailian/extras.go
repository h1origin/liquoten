package bailian

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

const extrasInterval = 6 * time.Hour

const extrasTimeout = 20 * time.Second

type addonSummaryResponse struct {
	RemainingCredits  float64 `json:"remainingCredits"`
	ActiveCount       int     `json:"activeCount"`
	TotalCredits      float64 `json:"totalCredits"`
	NearestExpireTime int64   `json:"nearestExpireTime"`
}

type subscriptionInfo struct {
	InstanceCode  string `json:"instanceCode"`
	SpecCode      string `json:"specCode"`
	RemainingDays int    `json:"remainingDays"`
	StartTime     int64  `json:"startTime"`
	EndTime       int64  `json:"endTime"`
	AutoRenewFlag bool   `json:"autoRenewFlag"`
	Status        string `json:"status"`
}

func (p *Provider) RefreshNow() {
	p.lastAddonAttempt = time.Time{}
	p.lastSubscriptionAttempt = time.Time{}
}

func (p *Provider) refreshExtras(ctx context.Context, now time.Time, monthFull bool) {
	if monthFull {
		p.refreshAddonSummary(ctx, now)
	} else if p.lastAddonAttempt.IsZero() || now.Sub(p.lastAddonAttempt) >= extrasInterval {
		p.refreshAddonSummary(ctx, now)
	}
	if p.lastSubscriptionAttempt.IsZero() || now.Sub(p.lastSubscriptionAttempt) >= extrasInterval {
		p.refreshSubscription(ctx, now)
	}
}

// extrasContext 给附加查询单独计时，不和用量查询共用剩余的超时。
func extrasContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), extrasTimeout)
}

func (p *Provider) refreshAddonSummary(ctx context.Context, now time.Time) {
	ctx, cancel := extrasContext(ctx)
	defer cancel()
	raw, err := p.queryAPI(ctx, addonSummaryAPI)
	if err != nil {
		p.lastAddonAttempt = now
		log.Printf("百炼加购额度查询失败: %v", err)
		return
	}
	var summary addonSummaryResponse
	if err := json.Unmarshal(raw, &summary); err != nil {
		p.lastAddonAttempt = now
		log.Printf("百炼加购额度解析失败: %v", err)
		return
	}
	p.lastAddonAttempt = now
	p.addonSummary = summary
	p.addonLoaded = true
}

func (p *Provider) refreshSubscription(ctx context.Context, now time.Time) {
	ctx, cancel := extrasContext(ctx)
	defer cancel()
	raw, err := p.queryAPI(ctx, subscriptionAPI)
	if err != nil {
		p.lastSubscriptionAttempt = now
		log.Printf("百炼套餐查询失败: %v", err)
		return
	}
	var sub subscriptionInfo
	if err := json.Unmarshal(raw, &sub); err != nil {
		p.lastSubscriptionAttempt = now
		log.Printf("百炼套餐解析失败: %v", err)
		return
	}
	p.lastSubscriptionAttempt = now
	p.subscription = sub
	p.subscriptionLoaded = true
}

func (p *Provider) appendExtras(bal *providers.Balance, now time.Time) {
	if p.addonLoaded && p.addonSummary.TotalCredits > 0 {
		expire := epochMs(&p.addonSummary.NearestExpireTime)
		if !expire.IsZero() && now.Before(expire) {
			remaining := p.addonSummary.RemainingCredits
			total := p.addonSummary.TotalCredits
			bal.Sections = append(bal.Sections, providers.Section{
				Name:      "addon-credits",
				UsedPct:   (total - remaining) / total * 100,
				Remaining: remaining,
				ResetAt:   expire,
			})
		}
	}
	if p.subscriptionLoaded && subscriptionActive(p.subscription) {
		end := epochMs(&p.subscription.EndTime)
		if !end.After(now) {
			return
		}
		name := "subscription"
		if p.subscription.AutoRenewFlag {
			name = "subscription-renewing"
		}
		bal.Sections = append(bal.Sections, providers.Section{
			Name:      name,
			UsedPct:   -1,
			Remaining: remainingDays(now, end),
			ResetAt:   end,
		})
	}
}

func subscriptionActive(s subscriptionInfo) bool {
	return s.EndTime > 0 && (s.Status == "" || s.Status == "VALID")
}

func remainingDays(now, end time.Time) float64 {
	if end.IsZero() || !end.After(now) {
		return 0
	}
	return math.Floor(end.Sub(now).Hours() / 24)
}
