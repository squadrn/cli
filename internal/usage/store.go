package usage

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/squadrn/squadrn/internal/fsx"
)

// WriteEvery is how often an unchanged observation is written again, to keep ObservedAt fresh.
const WriteEvery = time.Minute

// Store keeps one file per account in Dir (the squadrn state dir's usage/), named after the
// account's fixed identifiers: <provider>-<name>.json. Files are private to the user.
type Store struct {
	Dir string
}

func (s Store) path(providerID, name string) string {
	return filepath.Join(s.Dir, providerID+"-"+name+".json")
}

// Load returns the stored snapshot; ok is false when there is none.
func (s Store) Load(providerID, name string) (snap Snapshot, ok bool, err error) {
	b, err := os.ReadFile(s.path(providerID, name))
	if errors.Is(err, fs.ErrNotExist) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	if err := json.Unmarshal(b, &snap); err != nil {
		return Snapshot{}, false, err
	}
	return snap, true, nil
}

func (s Store) Save(providerID, name string, snap Snapshot) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteBytes(s.path(providerID, name), append(b, '\n'), 0o600)
}

// Record merges obs into the stored snapshot and writes it when a value changed or the stored
// one is at least WriteEvery older. A corrupt file is replaced.
func (s Store) Record(providerID, name string, obs Snapshot) (Snapshot, bool, error) {
	old, _, err := s.Load(providerID, name)
	if err != nil && !isSyntax(err) {
		return Snapshot{}, false, err
	}
	merged := Merge(old, obs)
	if SameValues(old, merged) && merged.ObservedAt.Sub(old.ObservedAt) < WriteEvery {
		return old, false, nil
	}
	return merged, true, s.Save(providerID, name, merged)
}

// Remove forgets an account (e.g. after it is removed or renamed).
func (s Store) Remove(providerID, name string) error {
	err := os.Remove(s.path(providerID, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func isSyntax(err error) bool {
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	return errors.As(err, &se) || errors.As(err, &te)
}
