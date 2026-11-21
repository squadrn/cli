package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/squadrn/squadrn-cli/internal/provider"
)

func TestProjectKey(t *testing.T) {
	dir := t.TempDir()
	real, _ := filepath.EvalSymlinks(dir)
	sub := filepath.Join(dir, ".claude", "worktrees", "feat-a")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := projectKey(sub)
	if err != nil {
		t.Fatal(err)
	}
	want := nonAlnum.ReplaceAllString(filepath.Join(real, ".claude", "worktrees", "feat-a"), "-")
	if got != want {
		t.Fatalf("projectKey = %q, want %q", got, want)
	}
	if k, _ := projectKey("/Users/x/.claude-personal"); runtime.GOOS != "windows" && k != "-Users-x--claude-personal" {
		t.Errorf("encoding = %q", k)
	}
}

func writeSession(t *testing.T, configDir, key, id string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(configDir, "projects", key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(`{"id":"`+id+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestLatestSessionAcrossConfigDirs(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeSession(t, a, "k", "s1", old)
	writeSession(t, b, "k", "s2", old.Add(time.Hour))
	writeSession(t, a, "other", "s3", old.Add(2*time.Hour))

	s, err := latestSession([]string{a, b}, "k")
	if err != nil || s.ID != "s2" || s.Owner != 1 {
		t.Fatalf("latestSession = %+v, %v", s, err)
	}
	if _, err := latestSession([]string{a, b}, "missing"); !errors.Is(err, provider.ErrNoSession) {
		t.Fatalf("err = %v", err)
	}
}

const sid = "0b9c3a52-7d0e-4c1f-9a43-5b6e2f1d8c70"

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFindSession(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeSession(t, a, "-long-folder", sid, old)
	writeSession(t, b, "-long-folder", sid, old.Add(time.Hour))
	s, err := findSession([]string{a, b}, sid)
	if err != nil || s.Owner != 1 || projectDir(s) != "-long-folder" || s.ID != sid || !s.ModTime.Equal(old.Add(time.Hour)) {
		t.Fatalf("findSession = %+v, %v", s, err)
	}
	if _, err := findSession([]string{a}, "../x"); !errors.Is(err, provider.ErrBadSessionID) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := findSession([]string{a}, "11111111-2222-3333-4444-555555555555"); !errors.Is(err, provider.ErrNoSession) {
		t.Fatalf("missing: %v", err)
	}
	if provider.Claude.SessionID.MatchString("*") || !provider.Claude.SessionID.MatchString(sid) {
		t.Fatal("Spec.SessionID")
	}
}

func TestLatestSessionInFallsBackToCwd(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "cfg")
	wt := filepath.Join(root, "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, _ := json.Marshal(wt)
	write(t, filepath.Join(cfg, "projects", "-shortened-123", "s1.jsonl"), `{"type":"summary"}`+"\n"+`{"cwd":`+string(cwd)+`}`+"\n")
	write(t, filepath.Join(cfg, "projects", "-other", "s2.jsonl"), `{"cwd":"/elsewhere"}`+"\n")
	write(t, filepath.Join(cfg, "projects", "-junk", "s3.jsonl"), "not json")
	s, err := latestSessionIn([]string{cfg}, wt)
	if err != nil || s.ID != "s1" || projectDir(s) != "-shortened-123" {
		t.Fatalf("fallback = %+v, %v", s, err)
	}
	// The computed folder wins when it exists.
	key, _ := projectKey(wt)
	writeSession(t, cfg, key, "s4", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	if s, err := latestSessionIn([]string{cfg}, wt); err != nil || s.ID != "s4" {
		t.Fatalf("by key = %+v, %v", s, err)
	}
	if _, err := latestSessionIn([]string{cfg}, filepath.Join(root, "none")); !errors.Is(err, provider.ErrNoSession) {
		t.Fatalf("none: %v", err)
	}
}

func TestTranscriptCwdAndLastModel(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	write(t, p, `{"type":"user","cwd":"/r"}
{"type":"assistant","message":{"model":"claude-opus-4-7"}}
{"type":"user","message":{"content":"assistant"}}
{"type":"assistant","message":{"model":"claude-sonnet-4-6"}}
{"type":"assistant","message":{}}
`)
	if got := transcriptCwd(p); got != "/r" {
		t.Errorf("cwd = %q", got)
	}
	if got := lastModel(p); got != "claude-sonnet-4-6" {
		t.Errorf("model = %q", got)
	}
	if transcriptCwd(p+"x") != "" || lastModel(p+"x") != "" {
		t.Error("missing file")
	}
	write(t, p, `{"type":"summary"}`)
	if transcriptCwd(p) != "" {
		t.Error("cwd without one")
	}
}

// fixture writes a conversation with companions in config dir src and returns it.
func fixture(t *testing.T, src, content string, mod time.Time) Session {
	t.Helper()
	p := filepath.Join(src, "projects", "-p", sid+".jsonl")
	write(t, p, content)
	write(t, filepath.Join(src, "projects", "-p", sid, "subagents", "a.jsonl"), "a")
	write(t, filepath.Join(src, "projects", "-p", sid, "tool-results", "r.txt"), "r")
	write(t, filepath.Join(src, "file-history", sid, "f@v1"), "v1")
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
	s, err := findSession([]string{src}, sid)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTransferSession(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	mod := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := fixture(t, src, "one\n", mod)
	if err := transferSession(s, dst, false); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(dst, "projects", "-p", sid+".jsonl")
	for path, want := range map[string]string{
		copied: "one\n",
		filepath.Join(dst, "projects", "-p", sid, "subagents", "a.jsonl"):  "a",
		filepath.Join(dst, "projects", "-p", sid, "tool-results", "r.txt"): "r",
		filepath.Join(dst, "file-history", sid, "f@v1"):                    "v1",
	} {
		if b, err := os.ReadFile(path); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v", path, b, err)
		}
	}
	if fi, _ := os.Stat(copied); !fi.ModTime().Equal(mod) {
		t.Errorf("mtime = %v", fi.ModTime())
	}

	// The source grew: append-only, allowed.
	s = fixture(t, src, "one\ntwo\n", mod.Add(time.Hour))
	write(t, filepath.Join(src, "file-history", sid, "f@v2"), "v2")
	if err := transferSession(s, dst, false); err != nil {
		t.Fatalf("append-only: %v", err)
	}
	if b, _ := os.ReadFile(copied); string(b) != "one\ntwo\n" {
		t.Errorf("after growth = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "file-history", sid, "f@v2")); string(b) != "v2" {
		t.Error("new checkpoint not copied")
	}

	// The target went on on its own: diverged, refused unless forced.
	write(t, copied, "one\nthree\n")
	if err := transferSession(s, dst, false); !errors.Is(err, provider.ErrDiverged) {
		t.Fatalf("diverged: %v", err)
	}
	write(t, copied, "one\ntwo\nthree\n") // longer than the source
	if err := transferSession(s, dst, false); !errors.Is(err, provider.ErrDiverged) {
		t.Fatalf("longer target: %v", err)
	}
	if err := transferSession(s, dst, true); err != nil {
		t.Fatalf("force: %v", err)
	}
	if b, _ := os.ReadFile(copied); string(b) != "one\ntwo\n" {
		t.Errorf("after force = %q", b)
	}

	// Same file: nothing to do.
	if err := transferSession(s, src, false); err != nil {
		t.Fatalf("same dir: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dst, "projects", "-p"))
	for _, e := range entries {
		if e.Name() != sid+".jsonl" && e.Name() != sid {
			t.Errorf("leftover %s", e.Name())
		}
	}
}

func TestTransferSessionVerifiesTheCopy(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	s := fixture(t, src, "content\n", time.Now())
	stageHook = func(p string) { _ = os.WriteFile(p, []byte("tampered\n"), 0o600) }
	defer func() { stageHook = nil }()
	if err := transferSession(s, dst, false); !errors.Is(err, ErrVerify) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "projects", "-p", sid+".jsonl")); !os.IsNotExist(err) {
		t.Fatal("unverified copy left in place")
	}
	stageHook = func(string) { _ = os.WriteFile(s.Path, []byte("content\nmore\n"), 0o600) }
	if err := transferSession(s, dst, false); !errors.Is(err, ErrVerify) {
		t.Fatalf("source changed: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dst, "projects", "-p", ".*")); len(left) > 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestRunningSessions(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	write(t, filepath.Join(a, "sessions", "10.json"), `{"pid":10,"sessionId":"s10","cwd":"/r","status":"busy","kind":"interactive","extra":1}`)
	write(t, filepath.Join(a, "sessions", "11.json"), `{"pid":11,"sessionId":"s11"}`) // dead
	write(t, filepath.Join(a, "sessions", "12.json"), `{"pid":"12"}`)
	write(t, filepath.Join(a, "sessions", "13.json"), `not json`)
	write(t, filepath.Join(a, "sessions", "14.json"), `{"pid":14}`)
	write(t, filepath.Join(a, "sessions", "x.txt"), `{"pid":15,"sessionId":"s15"}`)
	write(t, filepath.Join(b, "sessions", "3.json"), `{"pid":3,"sessionId":"s3","cwd":"/q"}`)
	alive := func(pid int) bool { return pid != 11 }
	got := runningSessions([]string{a, b, filepath.Join(root, "missing")}, alive)
	var desc []string
	for _, s := range got {
		desc = append(desc, fmt.Sprintf("%d:%d:%s:%s", s.Owner, s.PID, s.SessionID, s.Cwd))
	}
	want := []string{"0:10:s10:/r", "1:3:s3:/q"}
	if fmt.Sprint(desc) != fmt.Sprint(want) {
		t.Fatalf("runningSessions = %v, want %v", desc, want)
	}
}

func TestUnavailableModel(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	write(t, p, `{"type":"assistant","message":{"model":"claude-opus-4-7"}}`+"\n")
	s := provider.Session{Path: p}
	pro, max := provider.AuthStatus{SubscriptionType: "pro"}, provider.AuthStatus{SubscriptionType: "max"}
	if got := (CLI{}).UnavailableModel(s, pro); got != "claude-opus-4-7" {
		t.Errorf("opus on pro = %q", got)
	}
	if got := (CLI{}).UnavailableModel(s, max); got != "" {
		t.Errorf("opus on max = %q", got)
	}
	write(t, p, `{"type":"assistant","message":{"model":"claude-sonnet-4-6"}}`+"\n")
	if got := (CLI{}).UnavailableModel(s, pro); got != "" {
		t.Errorf("sonnet on pro = %q", got)
	}
}
