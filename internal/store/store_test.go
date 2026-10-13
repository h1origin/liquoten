package store

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lk/liquoten-cred/internal/providers"
)

func sampleBalances() []providers.Balance {
	return []providers.Balance{{
		Provider:  "bailian",
		FetchedAt: time.Now(),
		Sections: []providers.Section{
			{Name: "5-hour quota", UsedPct: 37.4, ResetAt: time.UnixMilli(1768104000000)},
			{Name: "1-week quota", UsedPct: 12.1, ResetAt: time.UnixMilli(1768363200000)},
		},
	}}
}

func TestWriteReadStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	in := sampleBalances()
	if err := s.WriteState(in); err != nil {
		t.Fatal(err)
	}
	out, err := s.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Provider != "bailian" {
		t.Fatalf("ReadState = %+v", out)
	}
	if out[0].Sections[0].Name != "5-hour quota" || out[0].Sections[0].UsedPct != 37.4 {
		t.Fatalf("Sections[0] = %+v", out[0].Sections[0])
	}
	if !out[0].Sections[0].ResetAt.Equal(in[0].Sections[0].ResetAt) {
		t.Fatalf("ResetAt = %v, want %v", out[0].Sections[0].ResetAt, in[0].Sections[0].ResetAt)
	}
	// 不应残留 tmp 文件
	if _, err := os.Stat(filepath.Join(dir, "state.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("state.json.tmp 应已被 rename 掉")
	}
}

func TestReadStateMissing(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.ReadState(); err == nil {
		t.Fatal("ReadState 应报错（daemon 尚未写入）")
	}
}

func TestAppendHistory(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	base := sampleBalances()
	t0 := base[0].FetchedAt

	write := func(at time.Time, pct float64) {
		t.Helper()
		b := sampleBalances()
		b[0].FetchedAt = at
		b[0].Sections[0].UsedPct = pct
		if err := s.AppendHistory(b); err != nil {
			t.Fatal(err)
		}
	}
	write(t0, 37.4)
	write(t0.Add(time.Minute), 37.4)
	write(t0.Add(2*time.Minute), 38)
	write(t0.Add(3*time.Minute), 38)
	write(t0.Add(2*time.Minute+HistoryHeartbeat), 38)

	if lines := countLines(t, filepath.Join(dir, "history.jsonl")); lines != 3 {
		t.Fatalf("history 行数 = %d, want 3（首条 + 变化 + 心跳）", lines)
	}
}

func TestAppendHistoryNewStoreWritesFirstReading(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := New(dir).AppendHistory(sampleBalances()); err != nil {
			t.Fatal(err)
		}
	}
	if lines := countLines(t, filepath.Join(dir, "history.jsonl")); lines != 2 {
		t.Fatalf("history 行数 = %d, want 2（重启后先写一条）", lines)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines++
	}
	return lines
}
