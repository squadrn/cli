package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/squadrn/squadrn/internal/grid"
	"github.com/squadrn/squadrn/internal/inuse"
	"github.com/squadrn/squadrn/internal/notify"
	"github.com/squadrn/squadrn/internal/provider"
)

// agentRow is one running agent: a session squadrn started (in-use registry), a tmux pane, or
// both. The JSON keeps the field names of the grid-only ps of rc.3.
type agentRow struct {
	// ID is the tmux pane and Session its tmux session ("" outside tmux).
	ID      string `json:"id,omitempty"`
	Session string `json:"session,omitempty"`
	Account string `json:"account"`
	// Name is the grid pane's or the worktree's name, else the repository's.
	Name    string `json:"name"`
	Command string `json:"command,omitempty"`
	PID     int    `json:"pid,omitempty"`
	// State is working, input, failed or done when the agent's hooks reported it; else what the
	// pane's title says (Gemini: input, working, ready).
	State string `json:"state,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
	title string
}

// running lists running agents: every session squadrn started, in a terminal or a tmux pane,
// and every grid pane.
func (a *app) running(ctx context.Context) ([]agentRow, error) {
	var rows []agentRow
	if hasTmux() {
		t := grid.Tmux{}
		panes, err := t.Panes(ctx, "")
		if err != nil {
			return nil, err
		}
		stated, _ := t.StatePanes(ctx)
		seen := map[string]bool{}
		for _, p := range append(panes, stated...) {
			if !seen[p.ID] {
				seen[p.ID] = true
				rows = append(rows, agentRow{ID: p.ID, Session: p.Session, Account: p.Account, Name: p.Name, Command: p.Command, PID: p.PID, State: p.State, Cwd: p.Path, title: p.Title})
			}
		}
	}
	accts, err := a.store.List()
	if err != nil {
		return nil, err
	}
	var plain []agentRow
	for _, acct := range accts {
		for _, e := range inuse.Active(acct.Dir) {
			r := agentRow{Account: e.Account, PID: e.PID, Cwd: e.Cwd, Name: e.Worktree}
			if r.Account == "" {
				r.Account = acct.Label()
			}
			if i := paneOf(rows, r); i >= 0 {
				fillRow(&rows[i], r)
				continue
			}
			plain = append(plain, r)
		}
	}
	sort.Slice(plain, func(i, j int) bool {
		if plain[i].Account != plain[j].Account {
			return plain[i].Account < plain[j].Account
		}
		return plain[i].PID < plain[j].PID
	})
	rows = append(rows, plain...)
	st := notifyStore(a.store.Home).Load()
	for i := range rows {
		r := &rows[i]
		if r.State == "" {
			if ag, ok := st.AgentAt(r.Account, r.Cwd); ok {
				r.State = ag.State
			}
		}
		if r.State == "" {
			r.State = titleState(r.Account, r.title)
		}
		if r.Name == "" && r.Cwd != "" {
			e := notify.Event{Cwd: r.Cwd}
			e.Locate(worktreeDirs())
			r.Name = e.Place()
		}
	}
	return rows, nil
}

// titleState is the state the terminal title of an agent of account's provider shows; "" when
// the provider sets none or the title is not one of its.
func titleState(account, title string) string {
	pid, _, _ := strings.Cut(account, "/")
	spec, ok := provider.Get(pid)
	if !ok || title == "" {
		return ""
	}
	for prefix, state := range spec.PaneTitleStates {
		if strings.HasPrefix(strings.TrimSpace(title), prefix) {
			return state
		}
	}
	return ""
}

// paneOf finds the pane running r's session: the same process, or the same account in the
// same directory.
func paneOf(rows []agentRow, r agentRow) int {
	for i, p := range rows {
		if p.PID != 0 && p.PID == r.PID {
			return i
		}
	}
	for i, p := range rows {
		if p.Account == r.Account && r.Cwd != "" && filepath.Clean(p.Cwd) == filepath.Clean(r.Cwd) {
			return i
		}
	}
	return -1
}

func fillRow(p *agentRow, r agentRow) {
	if p.Account == "" {
		p.Account = r.Account
	}
	if r.Cwd != "" {
		p.Cwd = r.Cwd
	}
	if p.Name == "" {
		p.Name = r.Name
	}
}

func (a *app) ps(asJSON bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := a.running(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		if rows == nil {
			rows = []agentRow{}
		}
		return a.printJSON(rows)
	}
	if len(rows) == 0 {
		a.out("no running agents\n")
		return nil
	}
	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ACCOUNT\tWORKTREE\tSTATE\tSESSION\tPROCESS")
	for _, r := range rows {
		session, process := orDash(r.Session), r.Command
		if r.ID != "" {
			session += " " + r.ID
		}
		if process == "" {
			process = "pid " + strconv.Itoa(r.PID)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", orDash(r.Account), orDash(r.Name), orDash(r.State), session, process)
	}
	return w.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
