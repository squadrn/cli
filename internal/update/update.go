// Package update decides when to look for a new release and whether to mention one. Checks run
// in the background at most once per Interval and are cached; a notice never blocks or forces.
package update

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"sprawling-agents/internal/fsx"
	"sprawling-agents/internal/release"
)

// Interval between background checks; failures retry after the same interval.
const Interval = 24 * time.Hour

// Channels: stable sees releases, beta also sees pre-releases (v1.2.0-rc.1).
const (
	Stable = "stable"
	Beta   = "beta"
)

// State is the cached result of the last check.
type State struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest,omitempty"` // newest release tag on Channel
	// Channel is the channel Latest was looked up on; a result for another channel is stale.
	Channel string `json:"channel,omitempty"`
	Err     string `json:"error,omitempty"`
	// NotifiedAt is when the notice was last shown, so it is not repeated on every command.
	NotifiedAt time.Time `json:"notified_at,omitzero"`
}

func Load(path string) State {
	var s State
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// Save is best-effort: a cache failure never fails a command.
func Save(path string, s State) {
	if b, err := json.Marshal(s); err == nil {
		_ = fsx.WriteBytes(path, b, 0o600)
	}
}

// Due reports whether a new background check should start for channel: never checked, the
// interval has passed, or the cached result belongs to another channel.
func Due(s State, now time.Time, channel string) bool {
	return now.Sub(s.CheckedAt) >= Interval || (s.Latest != "" && s.Channel != channel)
}

// Release is one published release as the source lists it.
type Release struct {
	Tag        string
	Prerelease bool
	Draft      bool
}

// Newest picks the highest version on channel. Drafts and unparsable tags are ignored.
func Newest(rels []Release, channel string) string {
	best := ""
	for _, r := range rels {
		if r.Draft || (r.Prerelease && channel != Beta) || !validTag(r.Tag) {
			continue
		}
		if c, _ := release.Compare(r.Tag, best); best == "" || c > 0 {
			best = r.Tag
		}
	}
	return best
}

func validTag(tag string) bool {
	_, ok := release.Compare(tag, tag)
	return ok
}

// Available returns the newer release on channel to mention for current, if any. A result
// cached for another channel is ignored; development builds (unparsable versions) are never
// nagged.
func Available(current string, s State, channel string) (string, bool) {
	if s.Latest == "" || s.Channel != channel {
		return "", false
	}
	c, ok := release.Compare(s.Latest, "v"+strings.TrimPrefix(current, "v"))
	return s.Latest, ok && c > 0
}

// Disabled reports whether checks are turned off by the environment: SPRAWL_NO_UPDATE_CHECK, or
// running under CI.
func Disabled(getenv func(string) string) bool {
	return getenv("SPRAWL_NO_UPDATE_CHECK") != "" || getenv("CI") != ""
}
