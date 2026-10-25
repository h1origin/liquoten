package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/h1origin/liquoten/internal/providers"
)

const Name = "codex"

const maxFilesScanned = 5

type Provider struct {
	home  string
	now   func() time.Time
	cache map[string]cached
}

type cached struct {
	mod  time.Time
	size int64
	ev   *event
}

func init() {
	providers.Register(Name, func(opts providers.Options) (providers.Provider, error) {
		return New(DefaultHome()), nil
	})
}

func DefaultHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}

func New(home string) *Provider {
	return &Provider{home: home, now: time.Now, cache: map[string]cached{}}
}

func (p *Provider) Name() string { return Name }

func (p *Provider) Query(ctx context.Context) (providers.Balance, error) {
	bal := providers.Balance{Provider: Name, FetchedAt: p.now()}

	files, err := p.recentSessions()
	if err != nil {
		return bal, err
	}
	var ev *event
	for _, f := range files {
		if ctx.Err() != nil {
			return bal, ctx.Err()
		}
		ev, err = p.lastRateLimits(f)
		if err != nil {
			return bal, err
		}
		if ev != nil {
			break
		}
	}
	if ev == nil {
		return bal, errors.New("尚无数据：需要先在 Codex 里对话一次（数据来自 ~/.codex/sessions）")
	}

	bal.FetchedAt = ev.Timestamp
	bal.Sections = sections(ev.Payload.RateLimits, ev.Timestamp, p.now())
	if len(bal.Sections) == 0 {
		return bal, errors.New("Codex 会话记录里没有用量窗口数据")
	}
	return bal, nil
}

type fileInfo struct {
	path string
	mod  time.Time
	size int64
}

func (p *Provider) recentSessions() ([]fileInfo, error) {
	root := filepath.Join(p.home, "sessions")
	var files []fileInfo
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == root {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, fileInfo{path: path, mod: info.ModTime(), size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("读取 Codex 会话目录 %s 失败: %w", root, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("尚无数据：找不到 Codex 会话记录（%s），请先安装 Codex 并对话一次", root)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > maxFilesScanned {
		files = files[:maxFilesScanned]
	}
	return files, nil
}

func (p *Provider) lastRateLimits(f fileInfo) (*event, error) {
	if c, ok := p.cache[f.path]; ok && c.mod.Equal(f.mod) && c.size == f.size {
		return c.ev, nil
	}
	file, err := os.Open(f.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	ev, err := parseLastRateLimits(file)
	if err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", filepath.Base(f.path), err)
	}
	p.cache[f.path] = cached{mod: f.mod, size: f.size, ev: ev}
	return ev, nil
}

type event struct {
	Timestamp time.Time `json:"timestamp"`
	Payload   struct {
		Type       string      `json:"type"`
		RateLimits *rateLimits `json:"rate_limits"`
	} `json:"payload"`
}

type rateLimits struct {
	Primary   *window  `json:"primary"`
	Secondary *window  `json:"secondary"`
	Credits   *credits `json:"credits"`
	PlanType  string   `json:"plan_type"`
}

type window struct {
	UsedPercent     *float64 `json:"used_percent"`
	WindowMinutes   int      `json:"window_minutes"`
	ResetsAt        int64    `json:"resets_at"`
	ResetsInSeconds int64    `json:"resets_in_seconds"`
}

type credits struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance"`
}

var marker = []byte(`"rate_limits"`)

func parseLastRateLimits(r io.Reader) (*event, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	var last *event
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && bytes.Contains(line, marker) {
			var ev event
			if jerr := json.Unmarshal(line, &ev); jerr == nil && ev.Payload.RateLimits != nil {
				last = &ev
			}
		}
		if err == io.EOF {
			return last, nil
		}
		if err != nil {
			return last, err
		}
	}
}

func sections(rl *rateLimits, ts, now time.Time) []providers.Section {
	var out []providers.Section
	for _, w := range []*window{rl.Primary, rl.Secondary} {
		if w == nil || w.UsedPercent == nil {
			continue
		}
		s := providers.Section{Name: windowName(w.WindowMinutes), UsedPct: *w.UsedPercent, Remaining: -1}
		var reset time.Time
		if w.ResetsAt > 0 {
			reset = time.Unix(w.ResetsAt, 0)
		} else if w.ResetsInSeconds > 0 && !ts.IsZero() {
			reset = ts.Add(time.Duration(w.ResetsInSeconds) * time.Second)
		}
		if !reset.IsZero() {
			if now.After(reset) {
				s.UsedPct = 0
			} else {
				s.ResetAt = reset
			}
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return windowOrder(out[i].Name) < windowOrder(out[j].Name) })
	if c := rl.Credits; c != nil && c.HasCredits && !c.Unlimited {
		if v, err := strconv.ParseFloat(c.Balance, 64); err == nil {
			out = append(out, providers.Section{Name: "credits", UsedPct: -1, Remaining: v})
		}
	}
	return out
}

func windowName(minutes int) string {
	switch minutes {
	case 300:
		return "5-hour quota"
	case 10080:
		return "1-week quota"
	}
	if minutes > 0 && minutes%1440 == 0 {
		return fmt.Sprintf("%d-day quota", minutes/1440)
	}
	if minutes > 0 && minutes%60 == 0 {
		return fmt.Sprintf("%d-hour quota", minutes/60)
	}
	return fmt.Sprintf("%d-minute quota", minutes)
}

func windowOrder(name string) int {
	switch name {
	case "5-hour quota":
		return 0
	case "1-week quota":
		return 1
	}
	return 2
}
