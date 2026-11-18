package update

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStateRoundTripAndDue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.json")
	if s := Load(path); !s.CheckedAt.IsZero() {
		t.Fatal("missing file should be empty")
	}
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	if !Due(State{}, now, Stable) {
		t.Fatal("never checked: due")
	}
	Save(path, State{CheckedAt: now, Latest: "v1.1.0", Channel: Stable})
	s := Load(path)
	if s.Latest != "v1.1.0" || !s.CheckedAt.Equal(now) || s.Channel != Stable {
		t.Fatalf("Load = %+v", s)
	}
	if Due(s, now.Add(time.Hour), Stable) || !Due(s, now.Add(25*time.Hour), Stable) {
		t.Fatal("Due interval")
	}
	if !Due(s, now.Add(time.Hour), Beta) {
		t.Fatal("a result for another channel must be refreshed")
	}
}

func TestNewest(t *testing.T) {
	rels := []Release{{Tag: "v1.0.0"}, {Tag: "v1.2.0-rc.1", Prerelease: true}, {Tag: "v1.1.0"}, {Tag: "v9.0.0", Draft: true}, {Tag: "nightly"}}
	if got := Newest(rels, Stable); got != "v1.1.0" {
		t.Errorf("stable = %s", got)
	}
	if got := Newest(rels, Beta); got != "v1.2.0-rc.1" {
		t.Errorf("beta = %s", got)
	}
	if got := Newest(nil, Stable); got != "" {
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
		if _, got := Available(tt.current, State{Latest: tt.latest, Channel: Stable}, Stable); got != tt.want {
			t.Errorf("Available(%s, %s) = %v", tt.current, tt.latest, got)
		}
	}
	if _, got := Available("1.0.0", State{Latest: "v1.1.0-rc.1", Channel: Beta}, Stable); got {
		t.Error("a beta result was offered on the stable channel")
	}
	if _, got := Available("1.0.0", State{Latest: "v1.1.0"}, Stable); got {
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
	env = map[string]string{"SPRAWL_NO_UPDATE_CHECK": "1"}
	if !Disabled(get) {
		t.Fatal("opt-out")
	}
}
