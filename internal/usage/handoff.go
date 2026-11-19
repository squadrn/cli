package usage

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/squadrn/cli/internal/fsx"
)

// Handoff is one conversation moved between accounts.
type Handoff struct {
	At      time.Time `json:"at"`
	Session string    `json:"session"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Cwd     string    `json:"cwd,omitempty"`
}

// Ledger is the append-only record of handoffs, one JSON object per line (the squadrn state
// dir's handoffs.jsonl, private to the user).
type Ledger struct {
	Path string
}

func (l Ledger) Append(h Handoff) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return fsx.AppendLine(l.Path, b, 0o600)
}

// Last returns the latest handoff from or to account.
func (l Ledger) Last(account string) (Handoff, bool, error) {
	return l.Latest(func(h Handoff) bool { return h.From == account || h.To == account })
}

// Latest returns the latest handoff match accepts; lines that do not parse are skipped.
func (l Ledger) Latest(match func(Handoff) bool) (Handoff, bool, error) {
	f, err := os.Open(l.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return Handoff{}, false, nil
	} else if err != nil {
		return Handoff{}, false, err
	}
	defer func() { _ = f.Close() }()
	var last Handoff
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		var h Handoff
		if json.Unmarshal(sc.Bytes(), &h) != nil || !match(h) {
			continue
		}
		if !found || !h.At.Before(last.At) {
			last, found = h, true
		}
	}
	return last, found, sc.Err()
}
