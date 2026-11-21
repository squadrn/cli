package notify

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/squadrn/squadrn-cli/internal/fsx"
)

// State is what the policy remembers between hook runs (state/notify.json).
type State struct {
	Sessions map[string]Last `json:"sessions"`
	// Agents is what each session last reported, for ps, next and focus.
	Agents map[string]Agent `json:"agents,omitempty"`
}

// Agent is where a session runs and its state (working, input, failed, done).
type Agent struct {
	State   string    `json:"state"`
	At      time.Time `json:"at"`
	Account string    `json:"account,omitempty"`
	Cwd     string    `json:"cwd,omitempty"`
	// Pane is the tmux pane (%3) and Socket the tmux server's socket the session runs in.
	Pane   string `json:"pane,omitempty"`
	Socket string `json:"socket,omitempty"`
	// Bundle is the macOS bundle id of the terminal app running the session.
	Bundle string `json:"bundle,omitempty"`
}

// SetAgent records a session's state; it reports whether the state changed.
func (st *State) SetAgent(session string, a Agent) bool {
	if session == "" || a.State == "" {
		return false
	}
	if st.Agents == nil {
		st.Agents = map[string]Agent{}
	}
	prev, ok := st.Agents[session]
	st.Agents[session] = a
	return !ok || prev.State != a.State || prev.Pane != a.Pane
}

// AgentAt returns the newest state recorded for a session of account working in cwd.
func (st State) AgentAt(account, cwd string) (Agent, bool) {
	var best Agent
	found := false
	for _, a := range st.Agents {
		if a.Account == account && samePath(a.Cwd, cwd) && (!found || a.At.After(best.At)) {
			best, found = a, true
		}
	}
	return best, found
}

func samePath(a, b string) bool {
	return a != "" && b != "" && filepath.Clean(a) == filepath.Clean(b)
}

// Last is a session's most recent notification. ReplaceID is the desktop notifier's id of it,
// where the notifier replaces by id (notify-send).
type Last struct {
	Kind      Kind      `json:"kind"`
	Reason    string    `json:"reason,omitempty"`
	Turn      string    `json:"turn,omitempty"`
	At        time.Time `json:"at"`
	ReplaceID string    `json:"replace_id,omitempty"`
}

func (st *State) record(e Event, now time.Time) {
	if e.Session == "" {
		return
	}
	if st.Sessions == nil {
		st.Sessions = map[string]Last{}
	}
	prev := st.Sessions[e.Session]
	st.Sessions[e.Session] = Last{Kind: e.Kind, Reason: e.Reason, Turn: e.Turn, At: now, ReplaceID: prev.ReplaceID}
}

// prune forgets sessions not heard from in a day.
func (st *State) prune(now time.Time) {
	for k, l := range st.Sessions {
		if now.Sub(l.At) > keep {
			delete(st.Sessions, k)
		}
	}
	for k, a := range st.Agents {
		if now.Sub(a.At) > keep {
			delete(st.Agents, k)
		}
	}
}

// Store keeps the state and the log in squadrn's state dir.
type Store struct{ Dir string }

func (s Store) statePath() string { return filepath.Join(s.Dir, "notify.json") }

// LogPath is the delivery log.
func (s Store) LogPath() string { return filepath.Join(s.Dir, "notify.log") }

// Update loads the state under a lock, lets fn change it and saves it. A state that cannot be
// read starts empty: notifications never fail because of it.
func (s Store) Update(now time.Time, fn func(*State)) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	unlock, err := fsx.Lock(s.statePath()+".lock", 2*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	st := s.Load()
	fn(&st)
	st.prune(now)
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return fsx.WriteBytes(s.statePath(), b, 0o600)
}

// Load reads the state; a missing or broken file is an empty state.
func (s Store) Load() State {
	var st State
	b, err := os.ReadFile(s.statePath())
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Sessions == nil {
		st.Sessions = map[string]Last{}
	}
	return st
}

// maxLog is the log size at which it moves to notify.log.1.
const maxLog = 128 << 10

// Entry is one line of the log.
type Entry struct {
	At     time.Time `json:"at"`
	Kind   Kind      `json:"kind"`
	Reason string    `json:"reason,omitempty"`
	// Result is "sent", "skipped" or "failed"; Why explains skipped and failed.
	Result string `json:"result"`
	Why    string `json:"why,omitempty"`
	Method string `json:"method,omitempty"`
	Title  string `json:"title,omitempty"`
}

// Log appends one entry. It holds titles (agent, account, repo), never agent text.
func (s Store) Log(e Entry) {
	if os.MkdirAll(s.Dir, 0o700) != nil {
		return
	}
	if fi, err := os.Stat(s.LogPath()); err == nil && fi.Size() > maxLog {
		_ = os.Rename(s.LogPath(), s.LogPath()+".1")
	}
	b, err := json.Marshal(e)
	if err == nil {
		_ = fsx.AppendLine(s.LogPath(), b, 0o600)
	}
}

// Recent returns up to n of the newest entries, oldest first.
func (s Store) Recent(n int) ([]Entry, error) {
	b, err := os.ReadFile(s.LogPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var out []Entry
	for i := max(0, len(lines)-n); i < len(lines); i++ {
		var e Entry
		if json.Unmarshal([]byte(lines[i]), &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}
