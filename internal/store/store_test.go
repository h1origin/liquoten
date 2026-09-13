package store

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lk/llmcred/internal/providers"
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
	for i := 0; i < 3; i++ {
		if err := s.AppendHistory(sampleBalances()); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(filepath.Join(dir, "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines++
	}
	if lines != 3 {
		t.Fatalf("history 行数 = %d, want 3", lines)
	}
}
