// Package store 负责把轮询结果落盘：
//   - state.json：原子写入的最新快照，SwiftBar 插件只读这个文件
//   - history.jsonl：每次轮询追加一行，为后续趋势分析/看板留数据
//
// v1 用 JSONL 而非 SQLite：只在读数变化时追加（不变则 15 分钟一条心跳），规模小，
// 追加式 JSONL 零依赖、可直接 tail/流式读取；v3 做看板时再评估迁移。
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lk/liquoten-cred/internal/providers"
)

const HistoryHeartbeat = 15 * time.Minute

// Store 封装缓存目录下的读写。
type Store struct {
	dir  string
	last map[string]historyEntry
}

// New 指定缓存目录。
func New(dir string) *Store { return &Store{dir: dir, last: map[string]historyEntry{}} }

// DefaultDir 返回默认缓存目录（可用 LIQUOTEN_CACHE_DIR 覆盖）。
func DefaultDir() string {
	if d := os.Getenv("LIQUOTEN_CACHE_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".liquoten"
	}
	return filepath.Join(home, ".cache", "liquoten")
}

func (s *Store) ensureDir() error { return os.MkdirAll(s.dir, 0o755) }

// WriteState 原子写入最新快照（tmp + rename），读者永远看不到半截文件。
func (s *Store) WriteState(balances []providers.Balance) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(balances, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "state.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, "state.json"))
}

// ReadState 读取最新快照。
func (s *Store) ReadState() ([]providers.Balance, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err != nil {
		return nil, err
	}
	var balances []providers.Balance
	if err := json.Unmarshal(data, &balances); err != nil {
		return nil, err
	}
	return balances, nil
}

type historyEntry struct {
	Time     time.Time           `json:"time"`
	Provider string              `json:"provider"`
	Err      string              `json:"error,omitempty"`
	Sections []providers.Section `json:"sections,omitempty"`
}

// AppendHistory 追加一轮轮询记录到 history.jsonl。
func (s *Store) AppendHistory(balances []providers.Balance) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "history.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, b := range balances {
		e := historyEntry{Time: b.FetchedAt, Provider: b.Provider, Err: b.Err, Sections: b.Sections}
		if prev, ok := s.last[b.Provider]; ok && sameReading(prev, e) && e.Time.Sub(prev.Time) < HistoryHeartbeat {
			continue
		}
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
		s.last[b.Provider] = e
	}
	return nil
}

func sameReading(a, b historyEntry) bool {
	if a.Err != b.Err || len(a.Sections) != len(b.Sections) {
		return false
	}
	for i := range a.Sections {
		x, y := a.Sections[i], b.Sections[i]
		if x.Name != y.Name || x.UsedPct != y.UsedPct || x.Remaining != y.Remaining || !x.ResetAt.Equal(y.ResetAt) {
			return false
		}
	}
	return true
}
