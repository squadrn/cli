package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sprawling-agents/internal/fsx"
)

type entry struct {
	name, body string
	mode       int64
	typ        byte
}

func writeTarball(t *testing.T, path string, entries []entry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{Name: e.name, Mode: mode, Size: int64(len(e.body)), Typeflag: typ, Linkname: "/etc/passwd"}
		if typ != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, c := range []interface{ Close() error }{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func releaseEntries(binary string) []entry {
	return []entry{
		{name: "sprawl", body: binary, mode: 0o755},
		{name: "plugin/.claude-plugin/plugin.json", body: `{"name":"sprawl","version":"0.0.0"}`},
		{name: "plugin/hooks/hooks.json", body: `{}`},
		{name: "plugin/bin/sprawl-hook", body: "#!/bin/sh\n", mode: 0o755},
		{name: ".claude-plugin/marketplace.json", body: `{"name":"sprawl"}`},
	}
}

func TestAssetName(t *testing.T) {
	if got := AssetName("v1.2.3", "darwin", "arm64"); got != "sprawl_1.2.3_darwin_arm64.tar.gz" {
		t.Fatal(got)
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sprawl_1_linux_amd64.tar.gz")
	if err := os.WriteFile(file, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("payload"))
	sums := filepath.Join(dir, ChecksumsName)
	write := func(body string) {
		if err := os.WriteFile(sums, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("deadbeef  other.tar.gz\n" + hex.EncodeToString(sum[:]) + "  sprawl_1_linux_amd64.tar.gz\n")
	if err := VerifyChecksum(sums, file); err != nil {
		t.Fatalf("valid checksum rejected: %v", err)
	}
	write(strings.Repeat("0", 64) + "  sprawl_1_linux_amd64.tar.gz\n")
	if err := VerifyChecksum(sums, file); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("tampered file accepted: %v", err)
	}
	write("deadbeef  other.tar.gz\n")
	if err := VerifyChecksum(sums, file); err == nil || !strings.Contains(err.Error(), "no checksum") {
		t.Fatalf("missing entry accepted: %v", err)
	}
}

func TestExtract(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "r.tar.gz")
	writeTarball(t, tarball, releaseEntries("bin"))
	dst := filepath.Join(dir, "out")
	if err := Extract(tarball, dst); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "plugin", "bin", "sprawl-hook")); err != nil || fi.Mode().Perm() != 0o755 {
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

func TestExtractCapsEntries(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "r.tar.gz")
	many := make([]entry, maxEntries+1)
	for i := range many {
		many[i] = entry{name: "d", typ: tar.TypeDir}
	}
	writeTarball(t, tarball, many)
	if err := Extract(tarball, filepath.Join(dir, "out")); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("entry cap: %v", err)
	}
}

func TestCompare(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v1.0.0", "1.0.0", 0, true},
		{"v1.0.0", "v1.0.1", -1, true},
		{"v1.10.0", "v1.9.9", 1, true},
		{"v2.0.0-rc.1", "v2.0.0", -1, true},
		{"v2.0.0", "v2.0.0-rc.1", 1, true},
		{"v2.0.0-rc.1", "v2.0.0-rc.2", -1, true},
		{"v2.0.0-rc.2", "v2.0.0-rc.1", 1, true},
		{"dev", "v1.0.0", 0, false},
		{"v1.0", "v1.0.0", 0, false},
		{"v1.x.0", "v1.0.0", 0, false},
	} {
		if got, ok := Compare(tt.a, tt.b); got != tt.want || ok != tt.ok {
			t.Errorf("Compare(%s, %s) = %d, %v", tt.a, tt.b, got, ok)
		}
	}
}

func TestExtractRejectsUnsafeEntries(t *testing.T) {
	for name, e := range map[string]entry{
		"traversal": {name: "../escape", body: "x"},
		"absolute":  {name: "/tmp/escape", body: "x"},
		"symlink":   {name: "link", typ: tar.TypeSymlink},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			tarball := filepath.Join(dir, "r.tar.gz")
			writeTarball(t, tarball, []entry{e})
			if err := Extract(tarball, filepath.Join(dir, "out")); err == nil {
				t.Fatal("unsafe entry accepted")
			}
		})
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

func TestInstall(t *testing.T) {
	home := t.TempDir()
	l := Layout{DataDir: filepath.Join(home, "data", "sprawl"), BinDir: filepath.Join(home, "bin")}
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
	if err := json.Unmarshal(b, &manifest); err != nil || manifest["version"] != "1.0.0" || manifest["name"] != "sprawl" {
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
	l := Layout{DataDir: filepath.Join(home, "data", "sprawl"), BinDir: filepath.Join(home, "bin")}
	legacy := filepath.Join(home, "data", "squad", "bin", "squad")
	if err := os.MkdirAll(l.BinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := func(name, target string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(l.BinDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	link("squad", legacy)          // previous install: ours, replaced silently
	link("other", "/usr/bin/true") // unrelated link: left alone
	if err := os.WriteFile(filepath.Join(l.BinDir, "ai"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err) // unrelated file under a wanted name: moved aside
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	res, err := l.LinkCommands([]string{"sprawl", "ai", "squad"}, []string{legacy}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"sprawl", "ai", "squad"} {
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

	// Rename: sprawl → ai only. Links of ours under dropped names are removed.
	res, err = l.LinkCommands([]string{"ai"}, []string{legacy}, now)
	if err != nil || len(res.Created) != 0 {
		t.Fatalf("relink: %+v, %v", res, err)
	}
	if len(res.Removed) != 2 {
		t.Errorf("Removed = %v", res.Removed)
	}
	for _, gone := range []string{"sprawl", "squad"} {
		if _, err := os.Lstat(filepath.Join(l.BinDir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s still linked", gone)
		}
	}
}

func TestInstallRejectsIncompleteRelease(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "sprawl"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	l := Layout{DataDir: filepath.Join(t.TempDir(), "d"), BinDir: t.TempDir()}
	if err := l.Install(src, "v1"); err == nil || !strings.Contains(err.Error(), "not a sprawl release") {
		t.Fatalf("err = %v", err)
	}
}

func TestSnapshotRollbackAndUnlink(t *testing.T) {
	home := t.TempDir()
	l := Layout{DataDir: filepath.Join(home, "data", "sprawl"), BinDir: filepath.Join(home, "bin")}
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

	if _, err := l.LinkCommands([]string{"sprawl", "sra"}, nil, time.Now()); err != nil {
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

// TestAssetNamesStable pins the archive names existing installs and install.sh look for.
func TestAssetNamesStable(t *testing.T) {
	for _, tt := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "sprawl_1.2.3_linux_amd64.tar.gz"},
		{"linux", "arm64", "sprawl_1.2.3_linux_arm64.tar.gz"},
		{"darwin", "amd64", "sprawl_1.2.3_darwin_amd64.tar.gz"},
		{"darwin", "arm64", "sprawl_1.2.3_darwin_arm64.tar.gz"},
		{"windows", "amd64", "sprawl_1.2.3_windows_amd64.zip"},
	} {
		if got := AssetName("v1.2.3", tt.goos, tt.goarch); got != tt.want {
			t.Errorf("AssetName(%s/%s) = %s, want %s", tt.goos, tt.goarch, got, tt.want)
		}
	}
}
