package grid

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/squadrn/squadrn/internal/proc"
)

// Sessions and panes are identified by tmux user options, never by their names, so renaming
// the command (and with it new session names) never orphans a running grid.
const (
	optRepo    = "@squadrn_repo" // session: repository root the grid belongs to
	optAccount = "@squadrn_acct" // pane: account label, e.g. claude/personal
	optName    = "@squadrn_name" // pane: worktree name
	// fieldSep separates -F fields. Not a tab: tmux < 3.5 rewrites tabs in output to '_'.
	// Fields that may contain it (paths, session names, commands) are printed with the q:
	// modifier, which escapes it with a backslash; splitFields undoes that.
	fieldSep = "|"
)

// q prints a format variable shell-escaped, so fieldSep inside it cannot split the line.
func q(name string) string { return "#{q:" + name + "}" }

// splitFields splits a line of q-escaped fields at unescaped separators and unescapes them.
func splitFields(line string) []string {
	var fields []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
		case c == fieldSep[0]:
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(fields, cur.String())
}

// startDir escapes a directory for -c, which tmux expands as a format: '#' must be doubled or
// a path containing "#{" or "##" names another directory.
func startDir(dir string) string { return strings.ReplaceAll(dir, "#", "##") }

var unsafeSessionChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// sessionName is the preferred name of a repository's grid session: <prefix>-<repo dir name>.
func sessionName(prefix, root string) string {
	return prefix + "-" + unsafeSessionChars.ReplaceAllString(filepath.Base(root), "-")
}

type Pane struct {
	ID      string
	Session string
	Account string
	Name    string
	Command string
}

type Session struct {
	Name     string
	Repo     string
	Panes    int
	Attached bool
}

// ErrNoServer means no tmux server is running, so there are no grids.
var ErrNoServer = errors.New("no tmux server running")

// Runner runs one tmux invocation and returns its trimmed stdout.
type Runner func(ctx context.Context, args ...string) (string, error)

// Tmux drives the tmux server selected by the environment (TMUX_TMPDIR / default socket).
type Tmux struct {
	Run Runner // nil runs the tmux binary
}

func runTmux(ctx context.Context, args ...string) (string, error) {
	out, err := proc.Output(ctx, proc.Options{}, "tmux", args...)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting to") {
			return "", ErrNoServer
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (t Tmux) run(ctx context.Context, args ...string) (string, error) {
	if t.Run != nil {
		return t.Run(ctx, args...)
	}
	return runTmux(ctx, args...)
}

// batch runs several tmux commands in one invocation (`a \; b`): the server executes them in
// order without interleaving other clients, and only one process is started.
func (t Tmux) batch(ctx context.Context, cmds ...[]string) (string, error) {
	var args []string
	for i, c := range cmds {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, c...)
	}
	return t.run(ctx, args...)
}

// FindSession returns the grid session of root, or "" when none runs.
func (t Tmux) FindSession(ctx context.Context, root string) (string, error) {
	sessions, err := t.Sessions(ctx)
	if err != nil {
		return "", err
	}
	for _, s := range sessions {
		if s.Repo == root {
			return s.Name, nil
		}
	}
	return "", nil
}

// Sessions lists grid sessions (those tagged with a repository). No server means none.
func (t Tmux) Sessions(ctx context.Context) ([]Session, error) {
	format := strings.Join([]string{q("session_name"), q(optRepo), "#{session_attached}"}, fieldSep)
	out, err := t.run(ctx, "list-sessions", "-F", format)
	if errors.Is(err, ErrNoServer) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var sessions []Session
	for _, line := range strings.Split(out, "\n") {
		f := splitFields(line)
		if len(f) != 3 || f[1] == "" {
			continue
		}
		sessions = append(sessions, Session{Name: f[0], Repo: f[1], Attached: f[2] != "0" && f[2] != ""})
	}
	if len(sessions) == 0 {
		return sessions, nil
	}
	panes, err := t.run(ctx, "list-panes", "-a", "-F", q("session_name"))
	if err != nil && !errors.Is(err, ErrNoServer) {
		return nil, err
	}
	count := map[string]int{}
	for _, name := range strings.Split(panes, "\n") {
		count[splitFields(name)[0]]++
	}
	for i := range sessions {
		sessions[i].Panes = count[sessions[i].Name]
	}
	return sessions, nil
}

// Panes lists squadrn agent panes, of one session or ("") of every session.
func (t Tmux) Panes(ctx context.Context, session string) ([]Pane, error) {
	format := strings.Join([]string{"#{pane_id}", q("session_name"), q(optAccount), q(optName), q("pane_current_command")}, fieldSep)
	args := []string{"list-panes", "-F", format}
	if session == "" {
		args = append(args, "-a")
	} else {
		args = append(args, "-s", "-t", "="+session)
	}
	out, err := t.run(ctx, args...)
	if errors.Is(err, ErrNoServer) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		f := splitFields(line)
		if len(f) != 5 || f[3] == "" {
			continue
		}
		panes = append(panes, Pane{ID: f[0], Session: f[1], Account: f[2], Name: f[3], Command: f[4]})
	}
	return panes, nil
}

func tagCmds(pane string, a Agent) [][]string {
	return [][]string{
		{"set", "-p", "-t", pane, optAccount, a.Account},
		{"set", "-p", "-t", pane, optName, a.Name},
	}
}

// tagNewPane tags the pane the preceding command of the same batch created: tmux makes it the
// current pane, and no other client can run in between, so the pane is never seen untagged.
func tagNewPane(a Agent) [][]string {
	return [][]string{
		{"set", "-p", optAccount, a.Account},
		{"set", "-p", optName, a.Name},
	}
}

// Start opens a pane running command in dir for root's grid and returns the session it is in.
// Without a grid session for root it creates one named after prefix (cols×rows); when another
// squadrn creates the same session at the same time, the pane joins that one instead.
func (t Tmux) Start(ctx context.Context, prefix, root, dir string, cols, rows int, a Agent, command string) (string, error) {
	for attempt := 0; attempt < 20; attempt++ {
		session, err := t.FindSession(ctx, root)
		if err != nil {
			return "", err
		}
		if session != "" {
			cmds := [][]string{{"split-window", "-t", "=" + session + ":", "-c", startDir(dir), command}}
			cmds = append(cmds, tagNewPane(a)...)
			cmds = append(cmds, []string{"select-layout", "-t", "=" + session + ":", "tiled"})
			if _, err := t.batch(ctx, cmds...); err != nil && !isGone(err) {
				return "", err
			}
			return session, nil
		}
		name := sessionName(prefix, root)
		if attempt > 0 {
			name = fmt.Sprintf("%s-%d", name, attempt+1)
		}
		// One invocation: the session is tagged with its repository before any other client
		// can look for it. Targets in the same sequence cannot use the exact-match "=" prefix
		// (tmux has not indexed the new session yet); an exact name still matches first.
		cmds := [][]string{
			{"new-session", "-d", "-s", name, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows), "-c", startDir(dir), command},
			{"set", "-t", name, optRepo, root},
			{"set", "-t", name, "pane-border-status", "top"},
			{"set", "-t", name, "pane-border-format", " #{" + optAccount + "} · #{" + optName + "} "},
		}
		cmds = append(cmds, tagCmds(name+":", a)...)
		_, err = t.batch(ctx, cmds...)
		switch {
		case err == nil:
			return name, nil
		case strings.Contains(err.Error(), "duplicate session"):
			continue // taken: by a racing grid for root (found next round) or another repo
		case isGone(err):
			return name, nil // the command exited at once and took the session with it
		default:
			return "", err
		}
	}
	return "", errors.New("could not find a free tmux session name")
}

// isGone: the pane or session disappeared because its command exited immediately.
func isGone(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "can't find pane") || strings.Contains(msg, "can't find session") || strings.Contains(msg, "can't find window")
}

// Respawn replaces a pane's process with command, keeping the pane in place.
func (t Tmux) Respawn(ctx context.Context, pane, dir string, a Agent, command string) error {
	cmds := append([][]string{{"respawn-pane", "-k", "-t", pane, "-c", startDir(dir), command}}, tagCmds(pane, a)...)
	_, err := t.batch(ctx, cmds...)
	return err
}

// Kill stops a grid session and every agent in it.
func (t Tmux) Kill(ctx context.Context, session string) error {
	_, err := t.run(ctx, "kill-session", "-t", "="+session)
	return err
}

// Attach replaces the current process with a tmux client on session (switch-client inside tmux).
func (Tmux) Attach(session string) error {
	bin, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	args := []string{"tmux", "attach", "-t", "=" + session}
	if os.Getenv("TMUX") != "" {
		args = []string{"tmux", "switch-client", "-t", "=" + session}
	}
	return syscall.Exec(bin, args, os.Environ())
}

// ShellQuote renders argv as one POSIX shell command line (tmux runs pane commands via a shell).
func ShellQuote(argv ...string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
