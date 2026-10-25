// Package claudecode 读取 Claude Code 订阅（Pro/Max）的 5 小时 / 7 天滚动用量。
//
// 数据来源是 Claude Code 官方 statusline 输入里的 rate_limits 字段：
// `liquoten statusline` 作为 Claude Code 的 statusLine 命令，把它落盘到
// 缓存目录的 claudecode.json；本 provider 只读该文件，不碰 OAuth 凭证、
// 不调未公开接口。代价是数据只在使用 Claude Code 时更新。
package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

// Name 是注册表里的 provider 名称。
const Name = "claudecode"

// FileName 是 statusline 落盘的快照文件名（位于缓存目录）。
const FileName = "claudecode.json"

// Provider 读取 statusline 落盘的快照。
type Provider struct {
	path string
	now  func() time.Time
}

func init() {
	providers.Register(Name, func(opts providers.Options) (providers.Provider, error) {
		if opts.DataDir == "" {
			return nil, errors.New("缺少数据目录")
		}
		return New(opts.DataDir), nil
	})
}

// New 构造 provider，dir 为 statusline 写入快照的目录。
func New(dir string) *Provider {
	return &Provider{path: filepath.Join(dir, FileName), now: time.Now}
}

// Name 返回注册表名称。
func (p *Provider) Name() string { return Name }

func (p *Provider) PollInterval() time.Duration { return time.Minute }

// Query 读取快照；窗口已过重置时间则视为 0%。
func (p *Provider) Query(ctx context.Context) (providers.Balance, error) {
	bal := providers.Balance{Provider: Name, FetchedAt: p.now()}

	data, err := os.ReadFile(p.path)
	if errors.Is(err, fs.ErrNotExist) {
		return bal, errors.New("尚无数据：需把 Claude Code 的 statusLine 配置为 `liquoten statusline` 并发起一次对话")
	}
	if err != nil {
		return bal, err
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return bal, fmt.Errorf("解析 %s 失败: %w", FileName, err)
	}
	if !snap.CapturedAt.IsZero() {
		bal.FetchedAt = snap.CapturedAt
	}

	now := p.now()
	for _, w := range []struct {
		name string
		win  *window
	}{
		{"5-hour quota", snap.FiveHour},
		{"1-week quota", snap.SevenDay},
	} {
		if w.win == nil || w.win.UsedPct == nil {
			continue
		}
		s := providers.Section{Name: w.name, UsedPct: *w.win.UsedPct, Remaining: -1}
		if reset := time.Time(w.win.ResetsAt); !reset.IsZero() {
			if now.After(reset) {
				s.UsedPct = 0
			} else {
				s.ResetAt = reset
			}
		}
		bal.Sections = append(bal.Sections, s)
	}
	if len(bal.Sections) == 0 {
		return bal, errors.New("快照中没有 rate_limits 数据（仅 Pro/Max 订阅提供）")
	}
	return bal, nil
}

// Input 是 statusline stdin JSON 中用到的字段。
type Input struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	RateLimits *rateLimits `json:"rate_limits"`
}

type rateLimits struct {
	FiveHour *window `json:"five_hour,omitempty"`
	SevenDay *window `json:"seven_day,omitempty"`
}

type window struct {
	UsedPct  *float64 `json:"used_percentage"`
	ResetsAt epoch    `json:"resets_at"`
}

type snapshot struct {
	rateLimits
	CapturedAt time.Time `json:"captured_at"`
}

// epoch 读入兼容 Unix 秒与 RFC3339 字符串，写出统一为 Unix 秒。
type epoch time.Time

func (e *epoch) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return err
		}
		*e = epoch(t)
		return nil
	}
	var secs float64
	if err := json.Unmarshal(b, &secs); err != nil {
		return err
	}
	*e = epoch(time.Unix(int64(secs), 0))
	return nil
}

func (e epoch) MarshalJSON() ([]byte, error) {
	t := time.Time(e)
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.Unix())
}

// Capture 解析 statusline 输入；含 rate_limits 时合并进 dir 下的快照
// （本次缺席的窗口保留旧值），原子写入。
func Capture(raw []byte, dir string, now time.Time) (Input, error) {
	var in Input
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, err
	}
	if in.RateLimits == nil || (in.RateLimits.FiveHour == nil && in.RateLimits.SevenDay == nil) {
		return in, nil
	}

	path := filepath.Join(dir, FileName)
	var snap snapshot
	if old, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(old, &snap)
	}
	if in.RateLimits.FiveHour != nil {
		snap.FiveHour = in.RateLimits.FiveHour
	}
	if in.RateLimits.SevenDay != nil {
		snap.SevenDay = in.RateLimits.SevenDay
	}
	snap.CapturedAt = now

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return in, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return in, err
	}
	tmp, err := os.CreateTemp(dir, FileName+".*.tmp")
	if err != nil {
		return in, err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return in, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return in, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return in, err
	}
	return in, nil
}

// StatusLine 渲染 Claude Code 底部状态栏文本，如 "[Opus 5.5] 5h 23% · 周 41%"。
func StatusLine(in Input) string {
	var parts []string
	if in.Model.DisplayName != "" {
		parts = append(parts, "["+in.Model.DisplayName+"]")
	}
	var quota []string
	if rl := in.RateLimits; rl != nil {
		if w := rl.FiveHour; w != nil && w.UsedPct != nil {
			quota = append(quota, fmt.Sprintf("5h %.0f%%", *w.UsedPct))
		}
		if w := rl.SevenDay; w != nil && w.UsedPct != nil {
			quota = append(quota, fmt.Sprintf("周 %.0f%%", *w.UsedPct))
		}
	}
	if len(quota) > 0 {
		parts = append(parts, strings.Join(quota, " · "))
	}
	return strings.Join(parts, " ")
}
