//go:build !windows

package release

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/fsx"
)

func TestExtract(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "r.tar.gz")
	writeTarball(t, tarball, releaseEntries("bin"))
	dst := filepath.Join(dir, "out")
	if err := Extract(tarball, dst); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "plugin", "bin", "squadrn-hook")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("executable bit lost: %v %v", fi, err)
	}
}

func TestExtractSanitisesModes(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "r.tar.gz")
	writeTarball(t, tarball, []entry{{name: "suid", body: "x", mode: 0o4777}, {name: "world", body: "x", mode: 0o666}})
	dst := filepath.Join(dir, "out")
	if err := Extract(tarball, dst); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{"suid": 0o755, "world": 0o644} {
		fi, err := os.Stat(filepath.Join(dst, name))
		if err != nil || fi.Mode() != want {
			t.Errorf("%s mode = %v, want %v", name, fi.Mode(), want)
		}
	}
}

func TestInstall(t *testing.T) {
	home := t.TempDir()
	l := Layout{DataDir: filepath.Join(home, "data", "squadrn"), BinDir: filepath.Join(home, "bin")}
	if err := l.Install(unpacked(t, "v1-binary"), "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(l.Binary()); string(b) != "v1-binary" {
		t.Errorf("binary = %q", b)
	}
	if l.Marketplace() != l.DataDir {
		t.Error("marketplace not installed")
	}
	if fi, err := os.Stat(l.Binary()); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("binary mode: %v, %v", fi, err)
	}
	if b, err := os.ReadFile(filepath.Join(l.GitHooks(), "pre-push")); err != nil || !strings.Contains(string(b), l.Binary()) {
		t.Errorf("git hook shims not installed: %q, %v", b, err)
	}
	var manifest map[string]any
	b, _ := os.ReadFile(filepath.Join(l.DataDir, "plugin", ".claude-plugin", "plugin.json"))
	if err := json.Unmarshal(b, &manifest); err != nil || manifest["version"] != "1.0.0" || manifest["name"] != "squadrn" {
		t.Errorf("plugin.json = %s", b)
	}

	// Upgrade: replaces files and removes plugin files that disappeared.
	if err := os.WriteFile(filepath.Join(l.DataDir, "plugin", "stale.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.Install(unpacked(t, "v2-binary"), "v2.0.0"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(l.Binary()); string(b) != "v2-binary" {
		t.Errorf("binary after upgrade = %q", b)
	}
	if _, err := os.Stat(filepath.Join(l.DataDir, "plugin", "stale.txt")); !os.IsNotExist(err) {
		t.Error("stale plugin file survived the upgrade")
	}
	for _, leftover := range []string{"plugin.new", "plugin.old", ".claude-plugin.new", ".claude-plugin.old"} {
		if _, err := os.Stat(filepath.Join(l.DataDir, leftover)); !os.IsNotExist(err) {
			t.Errorf("%s left behind", leftover)
		}
	}
}

func TestLinkCommands(t *testing.T) {
	home := t.TempDir()
	l := Layout{DataDir: filepath.Join(home, "data", "squadrn"), BinDir: filepath.Join(home, "bin")}
	if err := os.MkdirAll(l.BinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := func(name, target string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(l.BinDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	link("other", "/usr/bin/true") // unrelated link: left alone
	if err := os.WriteFile(filepath.Join(l.BinDir, "ai"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err) // unrelated file under a wanted name: moved aside
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	res, err := l.LinkCommands([]string{"squadrn", "ai"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"squadrn", "ai"} {
		if target, _ := os.Readlink(filepath.Join(l.BinDir, n)); target != l.Binary() {
			t.Errorf("%s → %q", n, target)
		}
	}
	if len(res.BackedUp) != 1 || res.BackedUp[0] != filepath.Join(l.BinDir, "ai.bak-20260102-030405") {
		t.Errorf("BackedUp = %v", res.BackedUp)
	}
	if target, _ := os.Readlink(filepath.Join(l.BinDir, "other")); target != "/usr/bin/true" {
		t.Error("unrelated link was touched")
	}

	// Rename: squadrn → ai only. Links of ours under dropped names are removed.
	res, err = l.LinkCommands([]string{"ai"}, now)
	if err != nil || len(res.Created) != 0 {
		t.Fatalf("relink: %+v, %v", res, err)
	}
	if len(res.Removed) != 1 {
		t.Errorf("Removed = %v", res.Removed)
	}
	for _, gone := range []string{"squadrn"} {
		if _, err := os.Lstat(filepath.Join(l.BinDir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s still linked", gone)
		}
	}
}

func TestInstallRejectsIncompleteRelease(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "squadrn"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	l := Layout{DataDir: filepath.Join(t.TempDir(), "d"), BinDir: t.TempDir()}
	if err := l.Install(src, "v1"); err == nil || !strings.Contains(err.Error(), "not a squadrn release") {
		t.Fatalf("err = %v", err)
	}
}

func TestSnapshotRollbackAndUnlink(t *testing.T) {
	home := t.TempDir()
	l := Layout{DataDir: filepath.Join(home, "data", "squadrn"), BinDir: filepath.Join(home, "bin")}
	if err := l.Snapshot(l.Previous()); err != nil || fsx.IsDir(l.Previous()) {
		t.Fatalf("snapshot of nothing: %v", err)
	}
	if err := l.Install(unpacked(t, "v1-binary"), "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if l.Version() != "1.0.0" {
		t.Fatalf("Version = %q", l.Version())
	}
	if err := l.Snapshot(l.Previous()); err != nil {
		t.Fatal(err)
	}
	if err := l.Install(unpacked(t, "v2-binary"), "v2.0.0"); err != nil {
		t.Fatal(err)
	}
	if SnapshotVersion(l.Previous()) != "1.0.0" {
		t.Fatalf("snapshot version = %q", SnapshotVersion(l.Previous()))
	}
	// Rolling back installs the snapshot like any unpacked release.
	if err := l.Install(l.Previous(), SnapshotVersion(l.Previous())); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(l.Binary()); string(b) != "v1-binary" || l.Version() != "1.0.0" {
		t.Fatalf("after rollback: %q %q", b, l.Version())
	}

	if _, err := l.LinkCommands([]string{"squadrn", "sqd"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/elsewhere", filepath.Join(l.BinDir, "other")); err != nil {
		t.Fatal(err)
	}
	removed, err := l.Unlink()
	if err != nil || len(removed) != 2 {
		t.Fatalf("Unlink = %v, %v", removed, err)
	}
	if _, err := os.Lstat(filepath.Join(l.BinDir, "other")); err != nil {
		t.Error("foreign link removed")
	}
	if removed, err := (Layout{BinDir: filepath.Join(home, "none")}).Unlink(); err != nil || removed != nil {
		t.Errorf("missing bin dir: %v, %v", removed, err)
	}
}

func releaseEntries(binary string) []entry {
	return []entry{
		{name: "squadrn", body: binary, mode: 0o755},
		{name: "plugin/.claude-plugin/plugin.json", body: `{"name":"squadrn","version":"0.0.0"}`},
		{name: "plugin/hooks/hooks.json", body: `{}`},
		{name: "plugin/bin/squadrn-hook", body: "#!/bin/sh\n", mode: 0o755},
		{name: ".claude-plugin/marketplace.json", body: `{"name":"squadrn"}`},
	}
}

func unpacked(t *testing.T, binary string) string {
	t.Helper()
	dir := t.TempDir()
	tarball := filepath.Join(dir, "r.tar.gz")
	writeTarball(t, tarball, releaseEntries(binary))
	src := filepath.Join(dir, "src")
	if err := Extract(tarball, src); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestIncompleteAndInstalledCopy(t *testing.T) {
	home := t.TempDir()
	l := Layout{DataDir: filepath.Join(home, "data", "squadrn"), BinDir: filepath.Join(home, "bin")}
	if l.Incomplete("v1.0.0") {
		t.Fatal("nothing installed counts as incomplete")
	}
	if err := l.Install(unpacked(t, "v1-binary"), "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if l.Incomplete("v1.0.0") || !l.Incomplete("v2.0.0") {
		t.Fatal("Incomplete compares the installed VERSION")
	}
	if err := os.RemoveAll(filepath.Join(l.DataDir, "plugin")); err != nil {
		t.Fatal(err)
	}
	if !l.Incomplete("v1.0.0") {
		t.Fatal("a missing plugin dir is not incomplete")
	}
	if !l.IsInstalledCopy(l.Binary()) || !l.IsInstalledCopy(filepath.Join(l.BinDir, "sqd")) || l.IsInstalledCopy(filepath.Join(home, "squadrn")) {
		t.Fatal("IsInstalledCopy")
	}
}
