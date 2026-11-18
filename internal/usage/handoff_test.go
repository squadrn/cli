package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLedger(t *testing.T) {
	l := Ledger{Path: filepath.Join(t.TempDir(), "state", "handoffs.jsonl")}
	if _, ok, err := l.Last("claude/a"); ok || err != nil {
		t.Fatalf("empty ledger: %v %v", ok, err)
	}
	t0 := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	for _, h := range []Handoff{
		{At: t0, Session: "s1", From: "claude/a", To: "claude/b", Cwd: "/r"},
		{At: t0.Add(time.Hour), Session: "s2", From: "claude/b", To: "claude/c"},
	} {
		if err := l.Append(h); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := os.OpenFile(l.Path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("garbage\n")
	_ = f.Close()
	if fi, _ := os.Stat(l.Path); fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("mode = %v", fi.Mode())
	}
	if h, ok, _ := l.Last("claude/a"); !ok || h.Session != "s1" || h.Cwd != "/r" {
		t.Errorf("Last(a) = %+v %v", h, ok)
	}
	if h, ok, _ := l.Last("claude/b"); !ok || h.Session != "s2" {
		t.Errorf("Last(b) = %+v %v", h, ok)
	}
	if _, ok, _ := l.Last("claude/z"); ok {
		t.Error("Last(z) found one")
	}
	if _, _, err := (Ledger{Path: t.TempDir()}).Last("x"); err == nil {
		t.Error("reading a directory succeeded")
	}
}
