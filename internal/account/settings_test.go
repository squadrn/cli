package account

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/provider"
)

// jsonFormat is a SettingsFormat for tests (the real ones live in the provider packages).
type jsonFormat struct{}

func (jsonFormat) Decode(b []byte) (map[string]any, error) {
	var m map[string]any
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	return m, json.Unmarshal(b, &m)
}

func (jsonFormat) Encode(m map[string]any) ([]byte, error) { return json.MarshalIndent(m, "", " ") }
func (jsonFormat) Deep() []string                          { return []string{"permissions"} }

var _ provider.SettingsFormat = jsonFormat{}

func settingsFixture(t *testing.T, main string) (Store, Account) {
	t.Helper()
	s := newStore(t, "work")
	if main != "" {
		must(t, os.WriteFile(filepath.Join(s.MainDir(provider.Claude), "settings.json"), []byte(main), 0o600))
	}
	a, err := s.Get("work")
	must(t, err)
	return s, a
}

func readDoc(t *testing.T, a Account) map[string]any {
	t.Helper()
	b, err := os.ReadFile(SettingsPath(a))
	must(t, err)
	var m map[string]any
	must(t, json.Unmarshal(b, &m))
	return m
}

func writeDoc(t *testing.T, a Account, m map[string]any) {
	t.Helper()
	b, _ := json.Marshal(m)
	must(t, os.WriteFile(SettingsPath(a), b, 0o600))
}

func setMain(t *testing.T, s Store, doc string) {
	t.Helper()
	must(t, os.WriteFile(filepath.Join(s.MainDir(provider.Claude), "settings.json"), []byte(doc), 0o600))
}

func TestSyncSettingsFollowsMainAndKeepsUserChanges(t *testing.T) {
	s, a := settingsFixture(t, `{"model":"opus","theme":"dark","permissions":{"deny":["Read(./.env)"]}}`)
	now := time.Now()
	r, err := s.SyncSettings(a, jsonFormat{}, nil, now)
	must(t, err)
	if !r.Created || !slices.Equal(r.Written, []string{"model", "permissions.deny", "theme"}) {
		t.Fatalf("first sync = %+v", r)
	}
	if fi, err := os.Lstat(SettingsPath(a)); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("settings is not a file of its own: %v", err)
	}

	// Nothing changed: nothing written.
	if r, err := s.SyncSettings(a, jsonFormat{}, nil, now); err != nil || r.Changed() || len(r.Kept) > 0 {
		t.Fatalf("idle sync = %+v, %v", r, err)
	}

	// The user picks another model in the account; main changes model and theme, drops nothing
	// and adds a key.
	doc := readDoc(t, a)
	doc["model"] = "sonnet"
	doc["own"] = true
	writeDoc(t, a, doc)
	setMain(t, s, `{"model":"haiku","theme":"light","permissions":{"deny":["Read(./.env)"]},"new":1}`)
	r, err = s.SyncSettings(a, jsonFormat{}, nil, now)
	must(t, err)
	if !slices.Equal(r.Kept, []string{"model"}) || !slices.Equal(r.Written, []string{"new", "theme"}) {
		t.Fatalf("sync after changes = %+v", r)
	}
	doc = readDoc(t, a)
	if doc["model"] != "sonnet" || doc["theme"] != "light" || doc["own"] != true || doc["new"] != float64(1) {
		t.Fatalf("doc = %v", doc)
	}
	// Reported once: the key is the user's now.
	setMain(t, s, `{"model":"opus","theme":"light","permissions":{"deny":["Read(./.env)"]},"new":1}`)
	if r, err := s.SyncSettings(a, jsonFormat{}, nil, now); err != nil || len(r.Kept) > 0 || r.Changed() {
		t.Fatalf("second report = %+v, %v", r, err)
	}

	// A key main drops goes away where the user left it alone.
	setMain(t, s, `{"model":"opus","permissions":{"deny":["Read(./.env)"]}}`)
	r, err = s.SyncSettings(a, jsonFormat{}, nil, now)
	must(t, err)
	if !slices.Equal(r.Removed, []string{"new", "theme"}) {
		t.Fatalf("removed = %+v", r)
	}
	m, ok, err := ReadManaged(a.Dir)
	if err != nil || !ok || m.Keys["model"].Source != SourceUser || m.Keys["permissions.deny"].Source != SourceMain {
		t.Fatalf("record = %+v, %v, %v", m, ok, err)
	}
}

func TestSyncSettingsMergesLayers(t *testing.T) {
	s, a := settingsFixture(t, `{"permissions":{"deny":["Read(./.env)","Read(~/.ssh/**)"],"allow":["Bash(ls)"]},"cleanupPeriodDays":90}`)
	profile := Layer{Source: SourceProfile, Doc: map[string]any{
		"permissions":       map[string]any{"deny": []any{"Read(~/.ssh/**)", "Read(~/.aws/**)"}},
		"cleanupPeriodDays": 30,
	}}
	_, err := s.SyncSettings(a, jsonFormat{}, []Layer{profile}, time.Now())
	must(t, err)
	doc := readDoc(t, a)
	perms := doc["permissions"].(map[string]any)
	if got := perms["deny"].([]any); len(got) != 3 || got[0] != "Read(./.env)" || got[2] != "Read(~/.aws/**)" {
		t.Errorf("deny = %v", got)
	}
	if perms["allow"] == nil || doc["cleanupPeriodDays"] != float64(30) {
		t.Errorf("doc = %v", doc)
	}
	m, _, _ := ReadManaged(a.Dir)
	if m.Keys["cleanupPeriodDays"].Source != SourceProfile || m.Keys["permissions.allow"].Source != SourceMain {
		t.Errorf("record = %+v", m)
	}
}

func TestSyncSettingsMigratesALinkAndKeepsABackup(t *testing.T) {
	s, a := settingsFixture(t, `{"model":"opus"}`)
	main := filepath.Join(s.MainDir(provider.Claude), "settings.json")
	must(t, link(main, SettingsPath(a)))
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	r, err := s.SyncSettings(a, jsonFormat{}, nil, now)
	must(t, err)
	if r.Backup != SettingsPath(a)+".bak-20261120-090000" || r.Created {
		t.Fatalf("report = %+v", r)
	}
	if linksTo(SettingsPath(a), main) {
		t.Fatal("still a link")
	}
	if readDoc(t, a)["model"] != "opus" {
		t.Fatal("copy lost main's content")
	}
	if b, _ := os.ReadFile(main); string(b) != `{"model":"opus"}` {
		t.Fatalf("main changed: %s", b)
	}
	if _, err := os.Lstat(r.Backup); err != nil {
		t.Fatalf("backup: %v", err)
	}
}

func TestSyncSettingsAdoptsOwnSettingsAsTheUsers(t *testing.T) {
	s, a := settingsFixture(t, `{"model":"opus","theme":"dark"}`)
	writeDoc(t, a, map[string]any{"model": "sonnet", "theme": "dark"})
	r, err := s.SyncSettings(a, jsonFormat{}, nil, time.Now())
	must(t, err)
	if !slices.Equal(r.Kept, []string{"model"}) || r.Changed() || readDoc(t, a)["model"] != "sonnet" {
		t.Fatalf("report = %+v", r)
	}
}

func TestClaimedSettingIsLeftAlone(t *testing.T) {
	s, a := settingsFixture(t, `{"statusLine":"main"}`)
	_, err := s.SyncSettings(a, jsonFormat{}, nil, time.Now())
	must(t, err)
	writeDoc(t, a, map[string]any{"statusLine": "ours"})
	must(t, ClaimSetting(a, jsonFormat{}, "statusLine"))
	if r, err := s.SyncSettings(a, jsonFormat{}, nil, time.Now()); err != nil || r.Changed() || len(r.Kept) > 0 {
		t.Fatalf("claimed = %+v, %v", r, err)
	}
	writeDoc(t, a, map[string]any{"statusLine": "main"})
	must(t, ReleaseSetting(a, jsonFormat{}, "statusLine"))
	setMain(t, s, `{"statusLine":"new"}`)
	r, err := s.SyncSettings(a, jsonFormat{}, nil, time.Now())
	if err != nil || !slices.Equal(r.Written, []string{"statusLine"}) {
		t.Fatalf("released = %+v, %v", r, err)
	}
	must(t, SetManagedProfile(a, "standard", []string{"privacy"}))
	if m, _, _ := ReadManaged(a.Dir); m.Profile != "standard" || m.Keys["statusLine"].Source != SourceMain {
		t.Fatalf("record = %+v", m)
	}
}

func TestSyncSettingsSkipsMainAndBadFiles(t *testing.T) {
	s, a := settingsFixture(t, `{`)
	if _, err := s.SyncSettings(a, jsonFormat{}, nil, time.Now()); err == nil {
		t.Error("broken main settings accepted")
	}
	m, _ := s.Get("main")
	if r, err := s.SyncSettings(m, jsonFormat{}, nil, time.Now()); err != nil || r.Changed() {
		t.Errorf("main = %+v, %v", r, err)
	}
	must(t, os.WriteFile(filepath.Join(a.Dir, ManagedFile), []byte("x"), 0o600))
	if _, _, err := ReadManaged(a.Dir); err == nil {
		t.Error("broken record accepted")
	}
}

// privateFormat keeps "mcpServers" per account.
type privateFormat struct{ jsonFormat }

func (privateFormat) Private() []string { return []string{"mcpServers"} }

func TestSyncSettingsNeverTakesPrivateKeysFromMain(t *testing.T) {
	s, a := settingsFixture(t, `{"model":"m","mcpServers":{"x":{"env":{"TOKEN":"t"}}}}`)
	if _, err := s.SyncSettings(a, privateFormat{}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	doc := readDoc(t, a)
	if doc["model"] != "m" || doc["mcpServers"] != nil {
		t.Fatalf("got %v", doc)
	}
	doc["mcpServers"] = map[string]any{"own": map[string]any{}}
	writeDoc(t, a, doc)
	if _, err := s.SyncSettings(a, privateFormat{}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if readDoc(t, a)["mcpServers"] == nil {
		t.Fatal("the account's own private key was removed")
	}
}
