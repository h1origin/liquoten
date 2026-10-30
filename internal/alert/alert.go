// Package alert 实现阈值告警：
//   - 每项额度剩余降到各提醒点（默认剩余 50%、20%）时各发一次通知，回升（新周期）后复位
//   - [thresholds] 里按已用百分比配置的阈值作为额外提醒点
//   - 查询连续失败 3 次告警一次，成功即复位
//   - 已告警状态落盘，daemon 重启后不会立即重复轰炸
//   - 通知默认写入 notifications.jsonl，由 liquoten.app 投递系统通知
package alert

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

// Notifier 发出系统通知。默认通道是 FileNotifier（App 投递）；OSAScriptNotifier 是无 App 时的回退方案。
type Notifier func(title, message string) error

// OSAScriptNotifier 是 macOS 上的通知实现（无 App 时的回退方案）。
func OSAScriptNotifier(title, message string) error {
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, escape(message), escape(title))
	return exec.Command("osascript", "-e", script).Run()
}

// escape 转义 AppleScript 字符串里的反斜杠与双引号。
func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// Event 是写给 liquoten.app 的通知事件（notifications.jsonl 的一行）。
type Event struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
	At    int64  `json:"at"` // unix 毫秒
}

// FileNotifier 把告警事件追加写入 cacheDir/notifications.jsonl，由 App 读取并
// 通过 UNUserNotificationCenter 投递。事件只追加不删除，App 用 id 游标去重。
func FileNotifier(cacheDir string) (Notifier, error) {
	path := filepath.Join(cacheDir, "notifications.jsonl")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	var (
		mu  sync.Mutex
		seq int
	)
	return func(title, message string) error {
		mu.Lock()
		defer mu.Unlock()
		id := fmt.Sprintf("%d-%d", time.Now().UnixMilli(), seq)
		seq++
		line, err := json.Marshal(Event{ID: id, Title: title, Body: message, At: time.Now().UnixMilli()})
		if err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.Write(append(line, '\n'))
		return err
	}, nil
}

// Engine 是告警状态机。
type Engine struct {
	mu         sync.Mutex
	thresholds map[string]map[string]float64 // provider -> section -> 已用百分比阈值
	remaining  []float64                     // 剩余百分比提醒点，对所有额度生效
	alerted    map[string]bool               // "provider|section|已用阈值" -> 是否已告警
	now        func() time.Time
	failCount  map[string]int // provider -> 连续失败次数
	notify     Notifier
	statePath  string
}

const maxConsecutiveFailures = 3

const resetCreditsAlertWindow = 12 * time.Hour

const expiryAlertWindow = 3 * 24 * time.Hour

// New 构造引擎。remaining 是剩余百分比提醒点；cacheDir 用于持久化告警状态；notify 为 nil 时静默（测试用）。
func New(thresholds map[string]map[string]float64, remaining []float64, cacheDir string, notify Notifier) (*Engine, error) {
	e := &Engine{
		thresholds: thresholds,
		remaining:  remaining,
		now:        time.Now,
		alerted:    map[string]bool{},
		failCount:  map[string]int{},
		notify:     notify,
		statePath:  filepath.Join(cacheDir, "alert-state.json"),
	}
	if data, err := os.ReadFile(e.statePath); err == nil {
		_ = json.Unmarshal(data, &e.alerted)
	}
	return e, nil
}

// Check 处理一轮轮询结果，按需触发通知。
func (e *Engine) Check(balances []providers.Balance) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var firstErr error
	for _, b := range balances {
		if b.Err != "" {
			e.failCount[b.Provider]++
			if e.failCount[b.Provider] == maxConsecutiveFailures {
				msg := fmt.Sprintf("%s 查询连续失败 %d 次：%s", b.Provider, maxConsecutiveFailures, b.Err)
				if err := e.fire("liquoten 告警", msg); err != nil {
					firstErr = err
				}
			}
			continue
		}
		e.failCount[b.Provider] = 0

		for _, sec := range b.Sections {
			switch sec.Name {
			case "reset-credits":
				if err := e.checkResetCredits(b.Provider, sec); err != nil && firstErr == nil {
					firstErr = err
				}
				continue
			case "subscription":
				if err := e.checkSubscription(b.Provider, sec); err != nil && firstErr == nil {
					firstErr = err
				}
				continue
			case "addon-credits":
				if err := e.checkAddonCredits(b.Provider, sec); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			if sec.UsedPct < 0 {
				continue
			}
			// 一次跨过多个提醒点时只发最紧的那一条，其余一并标记。
			var crossed float64
			for _, th := range e.levels(b.Provider, sec.Name) {
				key := fmt.Sprintf("%s|%s|%g", b.Provider, sec.Name, th)
				switch {
				case sec.UsedPct >= th && !e.alerted[key]:
					e.alerted[key] = true
					crossed = th
				case sec.UsedPct < th:
					e.alerted[key] = false
				}
			}
			if crossed > 0 {
				if err := e.fire("额度提醒", e.message(b.Provider, sec, crossed)); err != nil {
					firstErr = err
				}
			}
		}
	}

	e.pruneExpired()
	if err := e.persist(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// expiryKinds 是按到期时间去重的提醒，键的最后一段是到期时间的 Unix 秒。
var expiryKinds = map[string]bool{"reset-credits": true, "addon-credits-expire": true, "subscription": true}

const expiredKeyRetention = 7 * 24 * time.Hour

// pruneExpired 删掉到期已超过一周的到期类提醒记录，避免状态文件无限增长。
func (e *Engine) pruneExpired() {
	cutoff := e.now().Add(-expiredKeyRetention).Unix()
	for key := range e.alerted {
		parts := strings.Split(key, "|")
		if len(parts) != 3 || !expiryKinds[parts[1]] {
			continue
		}
		if ts, err := strconv.ParseInt(parts[2], 10, 64); err == nil && ts < cutoff {
			delete(e.alerted, key)
		}
	}
}

func (e *Engine) fire(title, msg string) error {
	if e.notify == nil {
		return nil
	}
	return e.notify(title, msg)
}

func (e *Engine) checkResetCredits(provider string, sec providers.Section) error {
	if sec.Remaining <= 0 || sec.ResetAt.IsZero() {
		return nil
	}
	now := e.now()
	left := sec.ResetAt.Sub(now)
	if left <= 0 || left >= resetCreditsAlertWindow {
		return nil
	}
	key := fmt.Sprintf("%s|%s|%d", provider, sec.Name, sec.ResetAt.Unix())
	if e.alerted[key] {
		return nil
	}
	e.alerted[key] = true
	msg := fmt.Sprintf("%s 有重置卡将于 %s 过期，可在 Codex 桌面 App 里使用", displayName(provider), e.formatTime(sec.ResetAt))
	return e.fire("额度提醒", msg)
}

func (e *Engine) checkAddonCredits(provider string, sec providers.Section) error {
	if sec.Remaining <= 0 || sec.ResetAt.IsZero() {
		return nil
	}
	now := e.now()
	left := sec.ResetAt.Sub(now)
	if left <= 0 || left >= expiryAlertWindow {
		return nil
	}
	key := fmt.Sprintf("%s|addon-credits-expire|%d", provider, sec.ResetAt.Unix())
	if e.alerted[key] {
		return nil
	}
	e.alerted[key] = true
	msg := fmt.Sprintf("%s加购额度将于 %s 过期，剩余 %g Credits", displayName(provider), e.formatTime(sec.ResetAt), sec.Remaining)
	return e.fire("额度提醒", msg)
}

func (e *Engine) checkSubscription(provider string, sec providers.Section) error {
	if sec.ResetAt.IsZero() {
		return nil
	}
	now := e.now()
	left := sec.ResetAt.Sub(now)
	if left <= 0 || left >= expiryAlertWindow {
		return nil
	}
	key := fmt.Sprintf("%s|subscription|%d", provider, sec.ResetAt.Unix())
	if e.alerted[key] {
		return nil
	}
	e.alerted[key] = true
	msg := fmt.Sprintf("%s Token Plan 套餐将于 %s 到期", displayName(provider), e.formatTime(sec.ResetAt))
	return e.fire("额度提醒", msg)
}

func (e *Engine) formatTime(t time.Time) string {
	now := e.now()
	layout := "1月2日 15:04"
	if y, m, d := t.Local().Date(); now.Local().Year() == y && now.Local().Month() == m && now.Local().Day() == d {
		layout = "15:04"
	}
	return t.Local().Format(layout)
}

func (e *Engine) persist() error {
	data, err := json.Marshal(e.alerted)
	if err != nil {
		return err
	}
	return os.WriteFile(e.statePath, data, 0o644)
}

// levels 返回该额度的已用百分比提醒点，升序去重。
func (e *Engine) levels(provider, section string) []float64 {
	var out []float64
	for _, r := range e.remaining {
		if r > 0 && r < 100 {
			out = append(out, 100-r)
		}
	}
	if v := e.thresholds[provider][section]; v > 0 {
		out = append(out, v)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (e *Engine) message(provider string, sec providers.Section, level float64) string {
	msg := fmt.Sprintf("%s %s剩余不到 %g%%（当前剩余 %.0f%%）", displayName(provider), sectionName(sec.Name), 100-level, max(0, 100-sec.UsedPct))
	if !sec.ResetAt.IsZero() {
		msg += "，" + e.formatTime(sec.ResetAt) + " 重置"
	}
	return msg
}

func displayName(provider string) string {
	switch provider {
	case "bailian":
		return "百炼"
	case "claudecode":
		return "Claude"
	case "codex":
		return "Codex"
	case "qoder":
		return "Qoder"
	case "qodercn":
		return "Qoder CN"
	}
	return provider
}

func sectionName(name string) string {
	switch name {
	case "5-hour quota":
		return "5 小时额度"
	case "1-week quota":
		return "每周额度"
	case "1-month quota":
		return "月度额度"
	case "reset-credits":
		return "重置卡"
	case "addon-credits":
		return "加购额度"
	case "subscription":
		return "套餐"
	}
	return name + " "
}
