package account

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/provider"
)

// homeSpec is a provider whose ConfigEnv names a home holding the CLI's state dir, like Gemini.
var homeSpec = provider.Spec{
	ID: "gem", Binary: "gem", MainDir: ".gem", DirPrefix: ".gem-", ConfigEnv: "GEM_HOME", StateDir: ".gem",
	Settings: "settings.json", Shared: []string{"GEM.md"}, SharedPerEntry: map[string][]string{"skills": nil},
	Credentials: []string{"oauth.json", "accounts.json"},
}

func TestStateDir(t *testing.T) {
	s := Store{Home: "/h", Providers: []provider.Spec{provider.Claude, homeSpec}}
	for _, tt := range []struct {
		a     Account
		state string
		creds []string
	}{
		{s.account(provider.Claude, Main), "/h/.claude", []string{"/h/.claude/.credentials.json"}},
		{s.account(provider.Claude, "work"), "/h/.claude-work", []string{"/h/.claude-work/.credentials.json"}},
		{s.account(homeSpec, Main), "/h/.gem", []string{"/h/.gem/oauth.json", "/h/.gem/accounts.json"}},
		{s.account(homeSpec, "work"), "/h/.gem-work/.gem", []string{"/h/.gem-work/.gem/oauth.json", "/h/.gem-work/.gem/accounts.json"}},
	} {
		if got := filepath.ToSlash(tt.a.StateDir()); !strings.HasSuffix(got, tt.state) {
			t.Errorf("%s: StateDir = %s, want %s", tt.a.Label(), got, tt.state)
		}
		var creds []string
		for _, c := range tt.a.CredentialFiles() {
			creds = append(creds, strings.TrimPrefix(filepath.ToSlash(c), filepath.ToSlash(filepath.VolumeName(c))))
		}
		if !slices.Equal(creds, tt.creds) {
			t.Errorf("%s: CredentialFiles = %v, want %v", tt.a.Label(), creds, tt.creds)
		}
	}
}

// A secondary account of a home-style CLI keeps squadrn's files (marker, record) in its dir and
// the CLI's files (shared links, settings) in its state dir.
func TestProvisionAndSyncUseTheStateDir(t *testing.T) {
	home := t.TempDir()
	s := Store{Home: home, Providers: []provider.Spec{homeSpec}}
	main := s.MainDir(homeSpec)
	must(t, os.MkdirAll(filepath.Join(main, "skills", "lint"), 0o755))
	must(t, os.WriteFile(filepath.Join(main, "GEM.md"), []byte("# main\n"), 0o600))
	must(t, os.WriteFile(filepath.Join(main, "settings.json"), []byte(`{"model":"m"}`), 0o600))
	a, err := s.Define("gem/work")
	must(t, err)
	if _, err := (Provisioner{Store: s, Agent: &fakeAgent{mcp: map[string]bool{}}}).Provision(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SyncSettings(a, jsonFormat{}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{Marker, ManagedFile} {
		if _, err := os.Stat(filepath.Join(a.Dir, p)); err != nil {
			t.Errorf("%s not in the account dir: %v", p, err)
		}
	}
	state := filepath.Join(a.Dir, ".gem")
	for _, p := range []string{"GEM.md", "skills/lint", "settings.json"} {
		if _, err := os.Lstat(filepath.Join(state, p)); err != nil {
			t.Errorf("%s not in the state dir: %v", p, err)
		}
		if _, err := os.Lstat(filepath.Join(a.Dir, p)); err == nil {
			t.Errorf("%s in the account dir", p)
		}
	}
	if got, err := s.Get("gem/work"); err != nil || got.Dir != a.Dir {
		t.Fatalf("Get = %v, %v", got, err)
	}
	if r := s.RefreshShared(a); len(r.Replaced)+len(r.Conflicts) > 0 {
		t.Fatalf("RefreshShared = %+v", r)
	}
}

func TestTrashFiles(t *testing.T) {
	s := Store{Home: t.TempDir(), Providers: []provider.Spec{homeSpec}}
	a := s.account(homeSpec, "work")
	must(t, os.MkdirAll(a.StateDir(), 0o700))
	creds := a.CredentialFiles()
	must(t, os.WriteFile(creds[0], []byte("{}"), 0o600))
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got, err := s.TrashFiles(a, creds, now)
	if err != nil || len(got) != 1 || filepath.Base(got[0]) != ".gem-work-oauth.json-20260102-030405" {
		t.Fatalf("TrashFiles = %v, %v", got, err)
	}
	if _, err := os.Stat(creds[0]); err == nil {
		t.Fatal("the file is still in the account")
	}
	if b, err := os.ReadFile(got[0]); err != nil || string(b) != "{}" {
		t.Fatalf("trashed file = %q, %v", b, err)
	}
	must(t, os.WriteFile(creds[0], []byte("{}"), 0o600))
	if _, err := s.TrashFiles(a, creds, now); err == nil {
		t.Fatal("a name taken in the trash was overwritten")
	}
}

// commentFormat is a SettingsFormat whose files may hold // comments.
type commentFormat struct{ jsonFormat }

func (commentFormat) HasComments(b []byte) bool { return strings.Contains(string(b), "//") }

func TestAddToListRefusesAFileWithComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := AddToList(path, commentFormat{}, "permissions.deny", []string{"x"}); err != nil {
		t.Fatalf("a missing file: %v", err)
	}
	orig := "// mine\n{}\n"
	must(t, os.WriteFile(path, []byte(orig), 0o600))
	if err := AddToList(path, commentFormat{}, "permissions.deny", []string{"x"}); !errors.Is(err, ErrCommented) {
		t.Fatalf("AddToList = %v, want ErrCommented", err)
	}
	if b, _ := os.ReadFile(path); string(b) != orig {
		t.Fatalf("the file was changed: %q", b)
	}
}

// filterFormat drops squadrn's hook entries from main, as the Claude and Gemini formats do.
type filterFormat struct{ jsonFormat }

func (filterFormat) Deep() []string              { return []string{"hooks"} }
func (filterFormat) FromMain(doc map[string]any) { provider.DropSquadrnHooks(doc) }

func TestSyncSettingsDropsSquadrnHooksFromMain(t *testing.T) {
	s, a := settingsFixture(t, `{"hooks":{
		"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"\"/d/plugin/bin/squadrn-hook\" guard-git"},{"type":"command","command":"lint"}]}],
		"Stop":[{"hooks":[{"type":"command","command":"squadrn.exe hook notify"}]}]}}`)
	if _, err := s.SyncSettings(a, filterFormat{}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(SettingsPath(a))
	if strings.Contains(string(b), "squadrn") || strings.Contains(string(b), "Stop") || !strings.Contains(string(b), `"lint"`) {
		t.Fatalf("account settings = %s", b)
	}
}
