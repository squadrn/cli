package grid

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/squadrn/squadrn/internal/worktree"
)

func TestAssign(t *testing.T) {
	tests := []struct {
		name   string
		specs  []string
		def    string
		spread []string
		want   []Agent
	}{
		{
			"default account",
			[]string{"feat-a", "fix-b"},
			"main", nil,
			[]Agent{{"main", "feat-a"}, {"main", "fix-b"}},
		},
		{
			"prefixes and labels",
			[]string{"personal:feat-a", "codex/work:fix-b", "chore-c"},
			"claude/main", nil,
			[]Agent{{"personal", "feat-a"}, {"codex/work", "fix-b"}, {"claude/main", "chore-c"}},
		},
		{
			"spread round-robin, prefixes keep their account",
			[]string{"a1", "x:a2", "a3", "a4"},
			"main",
			[]string{"main", "p"},
			[]Agent{{"main", "a1"}, {"x", "a2"}, {"p", "a3"}, {"main", "a4"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Assign(tt.specs, tt.def, tt.spread)
			if err != nil || !slices.Equal(got, tt.want) {
				t.Fatalf("Assign = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestAssignRejects(t *testing.T) {
	if _, err := Assign([]string{"feat-a", "p:feat-a"}, "main", nil); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("duplicate across accounts: %v", err)
	}
	if _, err := Assign([]string{"p:Bad_Name"}, "main", nil); !errors.Is(err, worktree.ErrInvalidName) {
		t.Errorf("bad name: %v", err)
	}
}

func TestSessionName(t *testing.T) {
	for root, want := range map[string]string{
		"/x/cmp-sm-service": "ai-cmp-sm-service",
		"/x/next.js-app":    "ai-next-js-app",
		"/x/a:b c":          "ai-a-b-c",
	} {
		if got := sessionName("ai", root); got != want {
			t.Errorf("SessionName(%q) = %q, want %q", root, got, want)
		}
	}
}

// fakeTmux records invocations and answers from a script of replies.
type fakeTmux struct {
	calls   [][]string
	replies func(args []string) (string, error)
}

func (f *fakeTmux) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	return f.replies(args)
}

func TestStartCreatesTaggedSessionInOneCall(t *testing.T) {
	f := &fakeTmux{replies: func(args []string) (string, error) {
		if args[0] == "list-sessions" {
			return "", ErrNoServer
		}
		return "", nil
	}}
	s, err := Tmux{Run: f.run}.Start(context.Background(), "srn", "/r/app", "/r/app", 80, 24, Agent{"claude/p", "feat-a"}, "cmd")
	if err != nil || s != "srn-app" {
		t.Fatalf("Start = %q, %v", s, err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls = %v", f.calls)
	}
	joined := strings.Join(f.calls[1], " ")
	for _, want := range []string{"new-session -d -s srn-app", "; set -t srn-app @squadrn_repo /r/app", "@squadrn_acct claude/p", "@squadrn_name feat-a"} {
		if !strings.Contains(joined, want) {
			t.Errorf("batch %q lacks %q", joined, want)
		}
	}
}

func TestStartJoinsSessionCreatedConcurrently(t *testing.T) {
	listed := 0
	f := &fakeTmux{replies: func(args []string) (string, error) {
		switch args[0] {
		case "list-sessions":
			listed++
			if listed == 1 {
				return "", nil
			}
			return "srn-app|/r/app|0", nil
		case "new-session":
			return "", errors.New("tmux new-session: exit status 1: duplicate session: srn-app")
		case "split-window":
			return "%7", nil
		}
		return "", nil
	}}
	s, err := Tmux{Run: f.run}.Start(context.Background(), "srn", "/r/app", "/r/app", 80, 24, Agent{"claude/p", "feat-b"}, "cmd")
	if err != nil || s != "srn-app" {
		t.Fatalf("Start = %q, %v (calls %v)", s, err, f.calls)
	}
	last := strings.Join(f.calls[len(f.calls)-1], " ")
	if !strings.HasPrefix(last, "split-window") || !strings.Contains(last, "; set -p @squadrn_name feat-b") || !strings.Contains(last, "select-layout") {
		t.Fatalf("pane not created and tagged in one batch: %q", last)
	}
}

func TestStartPicksNextNameForOtherRepo(t *testing.T) {
	f := &fakeTmux{replies: func(args []string) (string, error) {
		switch args[0] {
		case "list-sessions":
			return "srn-app|/other/app|0", nil
		case "new-session":
			if args[3] == "srn-app" {
				return "", errors.New("duplicate session: srn-app")
			}
		}
		return "", nil
	}}
	s, err := Tmux{Run: f.run}.Start(context.Background(), "srn", "/r/app", "/r/app", 80, 24, Agent{"claude/p", "x"}, "cmd")
	if err != nil || s != "srn-app-2" {
		t.Fatalf("Start = %q, %v", s, err)
	}
}

func TestSessionsAndPanes(t *testing.T) {
	f := &fakeTmux{replies: func(args []string) (string, error) {
		switch args[0] {
		case "list-sessions":
			return "plain||0\nsrn-app|/r/app|1\nodd\\|name|/r/a\\|b\\ c|0", nil
		case "list-panes":
			if args[1] == "-a" {
				return "plain\nsrn-app\nsrn-app\nsrn-app\nodd\\|name", nil
			}
			return "%1|srn-app|claude/main|feat-a|claude\n%2|srn-app|||zsh", nil
		}
		return "", errors.New("unexpected")
	}}
	tm := Tmux{Run: f.run}
	ss, err := tm.Sessions(context.Background())
	if err != nil || len(ss) != 2 || ss[0] != (Session{Name: "srn-app", Repo: "/r/app", Panes: 3, Attached: true}) ||
		ss[1] != (Session{Name: "odd|name", Repo: "/r/a|b c", Panes: 1}) {
		t.Fatalf("Sessions = %+v, %v", ss, err)
	}
	ps, err := tm.Panes(context.Background(), "srn-app")
	if err != nil || len(ps) != 1 || ps[0].Name != "feat-a" {
		t.Fatalf("Panes = %+v, %v", ps, err)
	}
	if err := tm.Kill(context.Background(), "srn-app"); err == nil {
		t.Fatal("Kill error not returned")
	}
	none := Tmux{Run: func(context.Context, ...string) (string, error) { return "", ErrNoServer }}
	if ss, err := none.Sessions(context.Background()); err != nil || ss != nil {
		t.Fatalf("no server: %v, %v", ss, err)
	}
	if ps, err := none.Panes(context.Background(), ""); err != nil || ps != nil {
		t.Fatalf("no server panes: %v, %v", ps, err)
	}
}
