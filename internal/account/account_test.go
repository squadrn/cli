package account

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/provider"
)

func newStore(t *testing.T, accounts ...string) Store {
	t.Helper()
	home := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))
	for _, a := range accounts {
		mkAccount(t, filepath.Join(home, ".claude-"+a))
	}
	return Store{Home: home}
}

// mkAccount creates an account dir as squadrn provisions it (with Marker).
func mkAccount(t *testing.T, dir string) {
	t.Helper()
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, Marker), nil, 0o600))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// codex is a stand-in second provider for resolution tests; it is not registered in squadrn.
var codex = provider.Spec{ID: "codex", Binary: "codex", MainDir: ".codex", DirPrefix: ".codex-", ConfigEnv: "CODEX_HOME"}

func twoProviders(s Store) Store {
	s.Providers = []provider.Spec{provider.Claude, codex}
	return s
}

func TestParseRef(t *testing.T) {
	for in, want := range map[string][2]string{
		"personal":        {"", "personal"},
		"claude/personal": {"claude", "personal"},
		"codex/work":      {"codex", "work"},
		"acme-personal":   {"", "acme-personal"},
	} {
		if p, n := parseRef(in); p != want[0] || n != want[1] {
			t.Errorf("parseRef(%q) = %q, %q", in, p, n)
		}
	}
}

func TestValidateName(t *testing.T) {
	for name, want := range map[string]error{
		"main": nil, "acme-2": nil, "ab": nil,
		"a": ErrInvalidName, "Personal": ErrInvalidName, "2fa": ErrInvalidName, "a_b": ErrInvalidName,
		strings.Repeat("a", 25): ErrInvalidName,
		"mcp":                   nil, "wt": ErrReserved, "self-update": ErrReserved, "config": ErrReserved,
	} {
		err := ValidateName(name)
		if (want == nil && err != nil) || (want != nil && !errors.Is(err, want)) {
			t.Errorf("ValidateName(%q) = %v, want %v", name, err, want)
		}
	}
}

func TestGet(t *testing.T) {
	s := newStore(t, "personal")
	for _, in := range []string{"personal", "claude/personal"} {
		a, err := s.Get(in)
		if err != nil || a.Label() != "claude/personal" || a.Dir != filepath.Join(s.Home, ".claude-personal") {
			t.Fatalf("Get(%q) = %+v, %v", in, a, err)
		}
		if a.ConfigDir() != a.Dir {
			t.Errorf("ConfigDir = %q", a.ConfigDir())
		}
	}
	m, err := s.Get("main")
	if err != nil || m.Label() != "claude/main" || m.ConfigDir() != "" {
		t.Errorf("main = %+v, %v; main must run with its config env unset", m, err)
	}
	var nf NotFoundError
	if _, err := s.Get("ghost"); !errors.As(err, &nf) || nf.Ref.String() != "claude/ghost" || !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(ghost) err = %v", err)
	}
	if _, err := s.Get("gemini/x1"); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("unknown provider err = %v", err)
	}
}

func TestGetAcrossProviders(t *testing.T) {
	s := twoProviders(newStore(t, "personal", "work"))
	mkAccount(t, filepath.Join(s.Home, ".codex-work"))
	mkAccount(t, filepath.Join(s.Home, ".codex-ops"))

	if a, err := s.Get("ops"); err != nil || a.Label() != "codex/ops" || a.Spec.ConfigEnv != "CODEX_HOME" {
		t.Errorf("unique name on another provider: %+v, %v", a, err)
	}
	if _, err := s.Get("work"); !errors.Is(err, ErrAmbiguous) || !strings.Contains(err.Error(), "claude/work, codex/work") {
		t.Errorf("ambiguous name err = %v", err)
	}
	if a, err := s.Get("codex/work"); err != nil || a.Dir != filepath.Join(s.Home, ".codex-work") {
		t.Errorf("explicit provider: %+v, %v", a, err)
	}
	if a, err := s.Get("main"); err != nil || a.Label() != "claude/main" {
		t.Errorf("main goes to the default provider: %+v, %v", a, err)
	}
	if _, err := s.Get("codex/main"); !errors.Is(err, ErrNotFound) {
		t.Errorf("another provider's main needs its dir: %v", err)
	}
	s.DefaultProvider = "codex"
	if a, err := s.Define("new-one"); err != nil || a.Label() != "codex/new-one" {
		t.Errorf("Define uses the default provider: %+v, %v", a, err)
	}
}

func TestExistsRoutesOnlyRealAccounts(t *testing.T) {
	s := newStore(t, "personal")
	for in, want := range map[string]bool{
		"personal": true, "claude/personal": true, "acme-personal": false, "main": true,
		"fix": false, "mcp": false, "--help": false, "codex/personal": false,
	} {
		if got := s.Exists(in); got != want {
			t.Errorf("Exists(%q) = %v", in, got)
		}
	}
}

func TestDefineRejectsMain(t *testing.T) {
	s := newStore(t)
	if _, err := s.Define("main"); !errors.Is(err, ErrReserved) {
		t.Errorf("Define(main) err = %v", err)
	}
	if a, err := s.Define("new-one"); err != nil || a.Label() != "claude/new-one" {
		t.Errorf("Define = %+v, %v", a, err)
	}
}

func TestListGroupsByProviderMainFirst(t *testing.T) {
	s := twoProviders(newStore(t, "zeta", "alpha", "Bad", "grid"))
	must(t, os.WriteFile(filepath.Join(s.Home, ".claude-file"), nil, 0o600))
	must(t, os.MkdirAll(filepath.Join(s.Home, ".codex"), 0o755))
	mkAccount(t, filepath.Join(s.Home, ".codex-b2"))
	got, err := s.List()
	must(t, err)
	var labels []string
	for _, a := range got {
		labels = append(labels, a.Label())
	}
	if !slices.Equal(labels, []string{"claude/main", "claude/alpha", "claude/zeta", "codex/main", "codex/b2"}) {
		t.Fatalf("List = %v", labels)
	}
}

func TestMoveToTrash(t *testing.T) {
	s := newStore(t, "old")
	t.Setenv("XDG_DATA_HOME", filepath.Join(s.Home, "data"))
	a, _ := s.Get("old")
	dst, err := s.MoveToTrash(a, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	must(t, err)
	if runtime.GOOS == "darwin" {
		if want := filepath.Join(s.Home, ".Trash", ".claude-old-20260102-030405"); dst != want {
			t.Errorf("dst = %q, want %q", dst, want)
		}
	} else {
		if want := filepath.Join(s.Home, "data", "Trash", "files", ".claude-old-20260102-030405"); dst != want {
			t.Errorf("dst = %q", dst)
		}
		info, err := os.ReadFile(filepath.Join(s.Home, "data", "Trash", "info", ".claude-old-20260102-030405.trashinfo"))
		must(t, err)
		if want := "[Trash Info]\nPath=" + filepath.ToSlash(a.Dir) + "\nDeletionDate=2026-01-02T03:04:05\n"; runtime.GOOS != "windows" && string(info) != want {
			t.Errorf("trashinfo = %q, want %q", info, want)
		}
	}
	if !fsx.IsDir(dst) {
		t.Errorf("trashed dir missing: %s", dst)
	}
	if s.Exists("old") {
		t.Error("account still exists after removal")
	}
	m, _ := s.Get("main")
	if _, err := s.MoveToTrash(m, time.Now()); !errors.Is(err, ErrReserved) {
		t.Errorf("removing main err = %v", err)
	}
}

type fakeAgent struct {
	provider.Agent
	provider.GuardInstaller
	calls     []string
	mcp       map[string]bool // servers present in the target account
	installed bool
}

func (f *fakeAgent) HasMCP(_ context.Context, _ string, name string) bool { return f.mcp[name] }

func (f *fakeAgent) AddMCP(_ context.Context, _ string, name string, _ []byte) error {
	f.calls = append(f.calls, "add mcp "+name)
	f.mcp[name] = true
	return nil
}

func (f *fakeAgent) InstallPlugin(_ context.Context, _ string, dir string) (bool, error) {
	f.calls = append(f.calls, "plugin "+dir)
	if f.installed {
		return false, nil
	}
	f.installed = true
	return true, nil
}

func provisionFixture(t *testing.T) (Store, Account, *fakeAgent, Provisioner) {
	t.Helper()
	s := newStore(t, "work")
	main := s.MainDir(provider.Claude)
	must(t, os.WriteFile(filepath.Join(main, "settings.json"), []byte("{}"), 0o600))
	must(t, os.WriteFile(filepath.Join(main, "CLAUDE.md"), []byte("# main\n"), 0o600))
	must(t, os.MkdirAll(filepath.Join(main, "agents"), 0o755))
	must(t, os.MkdirAll(filepath.Join(main, "skills", "find-skills"), 0o755))
	must(t, os.MkdirAll(filepath.Join(main, "skills", "synced"), 0o755))
	must(t, os.WriteFile(s.mcpConfig(provider.Claude), []byte(`{"mcpServers":{
		"codegraph":{"command":"codegraph","args":["serve"]},
		"figwright":{"command":"node","env":{}},
		"secret":{"command":"x","env":{"TOKEN":"t"}},
		"hdr":{"type":"http","url":"u","headers":{"Authorization":"b"}}}}`), 0o600))
	f := &fakeAgent{mcp: map[string]bool{}}
	p := Provisioner{Store: s, Agent: f, SharedMCP: []string{"codegraph", "figwright", "secret", "hdr", "absent"}, Marketplace: "/repo"}
	a, _ := s.Get("work")
	return s, a, f, p
}

func TestProvision(t *testing.T) {
	s, a, _, p := provisionFixture(t)
	r, err := p.Provision(context.Background(), a)
	must(t, err)

	if target, _ := os.Readlink(filepath.Join(a.Dir, "CLAUDE.md")); target != filepath.Join(s.MainDir(provider.Claude), "CLAUDE.md") {
		t.Errorf("CLAUDE.md link = %q", target)
	}
	if _, err := os.Lstat(filepath.Join(a.Dir, "skills", "find-skills")); err != nil {
		t.Errorf("skill not linked: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(a.Dir, "skills", "synced")); err == nil {
		t.Error("skills/synced must stay per account")
	}
	if _, err := os.Lstat(filepath.Join(a.Dir, "output-styles")); err == nil {
		t.Error("missing source output-styles must not create a dangling link")
	}
	if !slices.Equal(r.MCPAdded, []string{"codegraph", "figwright"}) ||
		!slices.Equal(r.MCPSkipped, []string{"secret", "hdr"}) ||
		!slices.Equal(r.MCPMissing, []string{"absent"}) {
		t.Errorf("MCP report = added %v skipped %v missing %v", r.MCPAdded, r.MCPSkipped, r.MCPMissing)
	}
	if !r.PluginInstalled {
		t.Error("plugin not installed")
	}
	if fi, err := os.Stat(a.Dir); runtime.GOOS != "windows" && (err != nil || fi.Mode().Perm() != 0o700) {
		t.Errorf("account dir mode = %v, %v", fi.Mode().Perm(), err)
	}
}

func TestRename(t *testing.T) {
	s := newStore(t, "old", "taken")
	old, err := s.Get("old")
	must(t, err)
	to, err := s.Rename(old, "new")
	must(t, err)
	if to.Label() != "claude/new" || !fsx.IsDir(to.Dir) || fsx.IsDir(old.Dir) {
		t.Fatalf("Rename = %+v", to)
	}
	if _, err := s.Rename(to, "taken"); err == nil {
		t.Error("renamed onto an existing account")
	}
	if _, err := s.Rename(to, "grid"); !errors.Is(err, ErrReserved) {
		t.Errorf("reserved target: %v", err)
	}
	main, err := s.Get("main")
	must(t, err)
	if _, err := s.Rename(main, "x1"); !errors.Is(err, ErrReserved) {
		t.Errorf("renamed main: %v", err)
	}
}

// Only squadrn's own commands are reserved; a provider subcommand is a valid account name.
func TestOnlySquadrnCommandsAreReserved(t *testing.T) {
	for _, n := range []string{"doctor", "grid", "usage"} {
		if err := ValidateName(n); !errors.Is(err, ErrReserved) {
			t.Errorf("ValidateName(%q) = %v", n, err)
		}
	}
	for _, n := range []string{"mcp", "setup-token", "plugin", "auth"} {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v", n, err)
		}
	}
}

func TestProvisionIsIdempotent(t *testing.T) {
	_, a, f, p := provisionFixture(t)
	_, err := p.Provision(context.Background(), a)
	must(t, err)
	before := len(f.calls)
	r, err := p.Provision(context.Background(), a)
	must(t, err)
	if len(r.Linked)+len(r.MCPAdded) != 0 || r.PluginInstalled {
		t.Errorf("second run changed things: %+v", r)
	}
	for _, c := range f.calls[before:] {
		if strings.HasPrefix(c, "add") {
			t.Errorf("second run mutated: %s", c)
		}
	}
}

func TestProvisionRemovesLinksToItemsMainNoLongerHas(t *testing.T) {
	s, a, _, p := provisionFixture(t)
	_, err := p.Provision(context.Background(), a)
	must(t, err)
	must(t, os.RemoveAll(filepath.Join(s.MainDir(provider.Claude), "agents")))
	foreign := filepath.Join(a.Dir, "mine")
	must(t, os.Symlink("/nowhere", foreign))

	r, err := p.Provision(context.Background(), a)
	must(t, err)
	if !slices.Equal(r.Unlinked, []string{filepath.Join(a.Dir, "agents")}) {
		t.Errorf("Unlinked = %v", r.Unlinked)
	}
	if _, err := os.Lstat(filepath.Join(a.Dir, "agents")); err == nil {
		t.Error("stale agents link kept")
	}
	if _, err := os.Lstat(foreign); err != nil {
		t.Error("a link squadrn does not manage must be left alone")
	}
}

func TestProvisionReportsConflicts(t *testing.T) {
	_, a, _, p := provisionFixture(t)
	must(t, os.WriteFile(filepath.Join(a.Dir, "CLAUDE.md"), []byte("{}"), 0o600))
	r, err := p.Provision(context.Background(), a)
	must(t, err)
	if !slices.Equal(r.Conflicts, []string{filepath.Join(a.Dir, "CLAUDE.md")}) {
		t.Errorf("Conflicts = %v", r.Conflicts)
	}
}

func TestProvisionRelinkKeepsABackup(t *testing.T) {
	s, a, _, p := provisionFixture(t)
	own := filepath.Join(a.Dir, "CLAUDE.md")
	must(t, os.MkdirAll(a.Dir, 0o700))
	must(t, os.WriteFile(own, []byte(`{"own":true}`), 0o600))
	if c := p.Conflicts(a); !slices.Equal(c, []string{own}) {
		t.Fatalf("Conflicts = %v", c)
	}
	p.Relink = true
	p.Now = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	r, err := p.Provision(context.Background(), a)
	must(t, err)
	bak := own + ".bak-20261119-100000"
	if r.Backups[own] != bak || len(r.Conflicts) != 0 {
		t.Fatalf("report = %+v", r)
	}
	if b, err := os.ReadFile(bak); err != nil || string(b) != `{"own":true}` {
		t.Fatalf("backup = %q, %v", b, err)
	}
	if !LinksTo(own, filepath.Join(s.MainDir(provider.Claude), "CLAUDE.md")) {
		t.Fatal("CLAUDE.md not linked after relink")
	}
	if c := p.Conflicts(a); len(c) != 0 {
		t.Fatalf("Conflicts after relink = %v", c)
	}
	// A backup with the same name is never overwritten.
	must(t, os.Remove(own))
	must(t, os.WriteFile(own, []byte("again"), 0o600))
	if _, err := p.Provision(context.Background(), a); err == nil {
		t.Fatal("relink overwrote an existing backup")
	}
	if b, _ := os.ReadFile(bak); string(b) != `{"own":true}` {
		t.Fatalf("backup changed: %q", b)
	}
}

// settings.json is not shared any more: Provision neither links it nor creates main's.
func TestProvisionLeavesSettingsAlone(t *testing.T) {
	s, a, _, p := provisionFixture(t)
	main := filepath.Join(s.MainDir(provider.Claude), "settings.json")
	must(t, os.Remove(main))
	_, err := p.Provision(context.Background(), a)
	must(t, err)
	if _, err := os.Lstat(main); err == nil {
		t.Error("main settings created")
	}
	if _, err := os.Lstat(filepath.Join(a.Dir, "settings.json")); err == nil {
		t.Error("account settings linked")
	}
}

func TestProvisionWithoutMarketplaceSkipsPlugin(t *testing.T) {
	_, a, f, p := provisionFixture(t)
	p.Marketplace = ""
	_, err := p.Provision(context.Background(), a)
	must(t, err)
	for _, c := range f.calls {
		if strings.Contains(c, "plugin") {
			t.Errorf("unexpected plugin call: %s", c)
		}
	}
}

func TestMarkerAdopt(t *testing.T) {
	s := newStore(t, "mine")
	must(t, os.MkdirAll(filepath.Join(s.Home, ".claude-tool"), 0o755)) // another tool's dir
	must(t, os.MkdirAll(filepath.Join(s.Home, ".claude-other"), 0o755))
	if s.Exists("tool") || s.Exists("other") {
		t.Fatal("a dir without the marker is an account")
	}
	un, err := s.Unmanaged()
	must(t, err)
	if len(un) != 2 || un[0].Name != "other" || un[1].Name != "tool" {
		t.Fatalf("Unmanaged = %+v", un)
	}
	a, err := s.Adopt("tool")
	must(t, err)
	if !s.Managed(a) {
		t.Fatal("adopted dir is not an account")
	}
	if _, err := s.Adopt("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Adopt(ghost) = %v", err)
	}
	if _, err := s.Adopt("main"); !errors.Is(err, ErrReserved) {
		t.Fatalf("Adopt(main) = %v", err)
	}
	m, _ := s.Get("main")
	if Mark(m) != nil || fsx.IsFile(filepath.Join(m.Dir, Marker)) {
		t.Fatal("main got a marker")
	}
}

func TestRefreshSharedReconcilesEntriesAndReportsReplaced(t *testing.T) {
	s, a, _, p := provisionFixture(t)
	_, err := p.Provision(context.Background(), a)
	must(t, err)
	if !fsx.IsFile(filepath.Join(a.Dir, Marker)) {
		t.Fatal("Provision did not mark the account")
	}
	main := s.MainDir(provider.Claude)
	must(t, os.MkdirAll(filepath.Join(main, "skills", "new-skill"), 0o755))
	must(t, os.RemoveAll(filepath.Join(main, "skills", "find-skills")))
	own := filepath.Join(a.Dir, "skills", "own")
	must(t, os.MkdirAll(own, 0o755))

	r := s.RefreshShared(a)
	if _, err := os.Lstat(filepath.Join(a.Dir, "skills", "new-skill")); err != nil {
		t.Error("new skill in main not linked")
	}
	if _, err := os.Lstat(filepath.Join(a.Dir, "skills", "find-skills")); err == nil {
		t.Error("link to a deleted skill kept")
	}
	if !fsx.IsDir(own) {
		t.Error("the account's own skill was touched")
	}
	if len(r.Conflicts)+len(r.Replaced) != 0 {
		t.Errorf("Refresh = %+v", r)
	}
	settings := filepath.Join(a.Dir, "CLAUDE.md")
	must(t, os.Remove(settings))
	must(t, os.WriteFile(settings, []byte(`{"own":true}`), 0o600))
	// Unix: a file replaced the link. Windows (copies): the copy was edited.
	if r := s.RefreshShared(a); !slices.Equal(append(r.Conflicts, r.Replaced...), []string{settings}) {
		t.Errorf("Refresh = %+v", r)
	}
	m, _ := s.Get("main")
	if r := s.RefreshShared(m); len(r.Replaced) != 0 {
		t.Error("main refreshed")
	}
}

func TestByConfigDir(t *testing.T) {
	s := twoProviders(newStore(t, "personal"))
	mkAccount(t, filepath.Join(s.Home, ".codex-personal"))
	if a, ok := s.ByConfigDir(provider.Claude, ""); !ok || a.Label() != "claude/main" {
		t.Errorf("unset dir = %+v, %v", a, ok)
	}
	if a, ok := s.ByConfigDir(provider.Claude, filepath.Join(s.Home, ".claude-personal")); !ok || a.Label() != "claude/personal" {
		t.Errorf("account dir = %+v, %v", a, ok)
	}
	if a, ok := s.ByConfigDir(codex, filepath.Join(s.Home, ".codex-personal")); !ok || a.Label() != "codex/personal" {
		t.Errorf("other provider = %+v, %v", a, ok)
	}
	if _, ok := s.ByConfigDir(provider.Claude, filepath.Join(s.Home, ".codex-personal")); ok {
		t.Error("another provider's dir matched")
	}
	if _, ok := s.ByConfigDir(provider.Claude, filepath.Join(s.Home, "elsewhere")); ok {
		t.Error("unknown dir matched")
	}
}
