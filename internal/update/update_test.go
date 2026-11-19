package update

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/config"
)

func TestStateRoundTripAndDue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.json")
	if s := Load(path); !s.CheckedAt.IsZero() {
		t.Fatal("missing file should be empty")
	}
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	if !Due(State{}, now, config.Stable) {
		t.Fatal("never checked: due")
	}
	Save(path, State{CheckedAt: now, Latest: "v1.1.0", Channel: config.Stable})
	s := Load(path)
	if s.Latest != "v1.1.0" || !s.CheckedAt.Equal(now) || s.Channel != config.Stable {
		t.Fatalf("Load = %+v", s)
	}
	if Due(s, now.Add(time.Hour), config.Stable) || !Due(s, now.Add(25*time.Hour), config.Stable) {
		t.Fatal("Due interval")
	}
	if !Due(s, now.Add(time.Hour), config.Beta) {
		t.Fatal("a result for another channel must be refreshed")
	}
}

func TestNewest(t *testing.T) {
	rels := []Release{{Tag: "v1.0.0"}, {Tag: "v1.2.0-rc.1", Prerelease: true}, {Tag: "v1.1.0"}, {Tag: "v9.0.0", Draft: true}, {Tag: "nightly"}}
	if got := Newest(rels, config.Stable); got != "v1.1.0" {
		t.Errorf("stable = %s", got)
	}
	if got := Newest(rels, config.Beta); got != "v1.2.0-rc.1" {
		t.Errorf("beta = %s", got)
	}
	if got := Newest(nil, config.Stable); got != "" {
		t.Errorf("none = %s", got)
	}
}

func TestAvailable(t *testing.T) {
	for _, tt := range []struct {
		current, latest string
		want            bool
	}{
		{"1.0.0", "v1.1.0", true},
		{"v1.1.0", "v1.1.0", false},
		{"1.2.0", "v1.1.0", false},
		{"dev", "v1.1.0", false},
		{"1.0.0", "", false},
	} {
		if _, got := Available(tt.current, State{Latest: tt.latest, Channel: config.Stable}, config.Stable); got != tt.want {
			t.Errorf("Available(%s, %s) = %v", tt.current, tt.latest, got)
		}
	}
	if _, got := Available("1.0.0", State{Latest: "v1.1.0-rc.1", Channel: config.Beta}, config.Stable); got {
		t.Error("a beta result was offered on the stable channel")
	}
	if _, got := Available("1.0.0", State{Latest: "v1.1.0"}, config.Stable); got {
		t.Error("a result without a channel was trusted")
	}
}

func TestDisabled(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if Disabled(get) {
		t.Fatal("disabled by default")
	}
	env["CI"] = "true"
	if !Disabled(get) {
		t.Fatal("CI")
	}
	env = map[string]string{"SQUADRN_NO_UPDATE_CHECK": "1"}
	if !Disabled(get) {
		t.Fatal("opt-out")
	}
}
