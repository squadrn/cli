package grid

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// privateTmux points tmux at a server of its own for this test (never the user's).
func privateTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sock, err := os.MkdirTemp("/tmp", "sqgt") // socket paths must stay short
	if err != nil {
		t.Skip("no /tmp")
	}
	t.Setenv("TMUX_TMPDIR", sock)
	t.Setenv("TMUX", "")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-server").Run()
		_ = os.RemoveAll(sock)
	})
}

func TestStartWithAwkwardPaths(t *testing.T) {
	privateTmux(t)
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tm := Tmux{}
	for i, name := range []string{"a#{b}|c d", "x##y"} {
		root := filepath.Join(base, name)
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		agent := Agent{Account: "claude/main", Name: "feat-a"}
		s, err := tm.Start(ctx, "srn", root, root, 80, 24, agent, "sleep 30")
		if err != nil {
			t.Fatalf("%s: Start: %v", name, err)
		}
		if _, err := tm.Start(ctx, "srn", root, root, 80, 24, Agent{Account: "claude/x", Name: "fix-b"}, "sleep 30"); err != nil {
			t.Fatalf("%s: second Start: %v", name, err)
		}
		if got, err := tm.FindSession(ctx, root); err != nil || got != s {
			t.Fatalf("%s: FindSession = %q, %v (want %q)", name, got, err, s)
		}
		panes, err := tm.Panes(ctx, s)
		if err != nil || len(panes) != 2 || panes[0].Name != "feat-a" || panes[1].Name != "fix-b" || panes[1].Account != "claude/x" {
			t.Fatalf("%s: Panes = %+v, %v", name, panes, err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			out, _ := exec.Command("tmux", "list-panes", "-s", "-t", "="+s, "-F", "#{pane_current_path}").Output()
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) == 2 && lines[0] == root && lines[1] == root {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: panes started in %q, want %q", name, lines, root)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if ss, err := tm.Sessions(ctx); err != nil || len(ss) != i+1 {
			t.Fatalf("Sessions = %+v, %v", ss, err)
		}
	}
}
