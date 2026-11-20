package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/squadrn/squadrn/internal/fsx"
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

// Remove rewrites the ledger without the handoffs from or to account and returns how many it
// dropped. Lines that do not parse are kept. A missing ledger is empty.
func (l Ledger) Remove(account string) (int, error) {
	return DropLines(l.Path, func(line []byte) bool {
		var h Handoff
		return json.Unmarshal(line, &h) == nil && (h.From == account || h.To == account)
	})
}

// DropLines atomically rewrites the line-oriented file path without the lines drop accepts and
// returns how many it dropped; the file is not written when none is. A missing file is empty.
func DropLines(path string, drop func([]byte) bool) (int, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	var kept []byte
	n := 0
	for line := range bytes.Lines(b) {
		if t := bytes.TrimRight(line, "\r\n"); len(t) > 0 && drop(t) {
			n++
			continue
		}
		kept = append(kept, line...)
	}
	if n == 0 {
		return 0, nil
	}
	return n, fsx.WriteBytes(path, kept, 0o600)
}
