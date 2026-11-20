package cli

import (
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/provider"
)

// copies is a SessionStore holding one conversation's copy per config dir, newest first wins.
type copies struct {
	provider.SessionStore
	at map[string]time.Time
}

func (c copies) FindSession(dirs []string, id string) (provider.Session, error) {
	best, found := provider.Session{}, false
	for i, d := range dirs {
		if t, ok := c.at[d]; ok && (!found || t.After(best.ModTime)) {
			best, found = provider.Session{ID: id, Path: d, ModTime: t, Owner: i}, true
		}
	}
	if !found {
		return provider.Session{}, provider.ErrNoSession
	}
	return best, nil
}

func TestFindSessionOfPrefersTheOwnerOnATie(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)
	dirs := []string{"main", "personal", "work"}
	tests := []struct {
		name  string
		at    map[string]time.Time
		owner int
		want  string
	}{
		{"tie goes to the owner", map[string]time.Time{"main": t0, "work": t0}, 2, "work"},
		{"a newer copy elsewhere wins", map[string]time.Time{"main": t0.Add(time.Minute), "work": t0}, 2, "main"},
		{"owner without a copy", map[string]time.Time{"main": t0}, 1, "main"},
		{"owner out of range", map[string]time.Time{"main": t0, "work": t0}, 5, "main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := findSessionOf(copies{at: tt.at}, dirs, "id", tt.owner)
			if err != nil {
				t.Fatal(err)
			}
			if s.Path != tt.want || dirs[s.Owner] != tt.want {
				t.Fatalf("got %s (owner %d), want %s", s.Path, s.Owner, tt.want)
			}
		})
	}
}
