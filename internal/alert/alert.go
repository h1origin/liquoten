// Package alert 实现阈值告警：
//   - 额度百分比升穿阈值时发一次通知（回到阈值以下后允许再次告警）
//   - 查询连续失败 3 次告警一次，成功即复位
//   - 已告警状态落盘，daemon 重启后不会立即重复轰炸
//   - 通知默认写入 notifications.jsonl，由 Liquoten.app 投递系统通知
package alert

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lk/liquoten-cred/internal/providers"
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

// Event 是写给 Liquoten.app 的通知事件（notifications.jsonl 的一行）。
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
	thresholds map[string]map[string]float64 // provider -> section -> 阈值百分比
	alerted    map[string]bool               // "provider|section" -> 是否已告警
	failCount  map[string]int                // provider -> 连续失败次数
	notify     Notifier
	statePath  string
}

const maxConsecutiveFailures = 3

// New 构造引擎。cacheDir 用于持久化告警状态；notify 为 nil 时静默（测试用）。
func New(thresholds map[string]map[string]float64, cacheDir string, notify Notifier) (*Engine, error) {
	e := &Engine{
		thresholds: thresholds,
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
			if sec.UsedPct < 0 {
				continue
			}
			key := b.Provider + "|" + sec.Name
			th := e.thresholdFor(b.Provider, sec.Name)
			if th <= 0 {
				continue
			}
			switch {
			case sec.UsedPct >= th && !e.alerted[key]:
				e.alerted[key] = true
				msg := fmt.Sprintf("%s %s 已用 %.1f%%（阈值 %.0f%%）", b.Provider, sec.Name, sec.UsedPct, th)
				if err := e.fire("liquoten 告警", msg); err != nil {
					firstErr = err
				}
			case sec.UsedPct < th:
				// 回到阈值以下：复位，允许下次再告警
				e.alerted[key] = false
			}
		}
	}

	if err := e.persist(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (e *Engine) fire(title, msg string) error {
	if e.notify == nil {
		return nil
	}
	return e.notify(title, msg)
}

func (e *Engine) persist() error {
	data, err := json.Marshal(e.alerted)
	if err != nil {
		return err
	}
	return os.WriteFile(e.statePath, data, 0o644)
}

func (e *Engine) thresholdFor(provider, section string) float64 {
	if m, ok := e.thresholds[provider]; ok {
		if v, ok := m[section]; ok {
			return v
		}
	}
	return 0
}
