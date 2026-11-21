// Package release verifies, unpacks and installs squadrn release archives.
//
// Installed layout:
//
//	$XDG_DATA_HOME/squadrn/          (~/.local/share/squadrn) — also the local plugin marketplace
//	├── bin/squadrn
//	├── .claude-plugin/marketplace.json
//	└── plugin/…
//	~/.local/bin/<command>, ~/.local/bin/<alias>… → ~/.local/share/squadrn/bin/squadrn
package release

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"cmp"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/squadrn/squadrn-cli/internal/config"
	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/githook"
)

// AssetName is the archive goreleaser publishes for one platform.
func AssetName(version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf(config.Slug+"_%s_%s_%s%s", strings.TrimPrefix(version, "v"), goos, goarch, ext)
}

// BinaryName (per platform: squadrn, squadrn.exe) is the executable inside a release and in the
// data dir; commands link to it.

const ChecksumsName = "checksums.txt"

// ErrUnsafeInstallDir: the install path cannot be used on this platform (CheckInstallDir).
var ErrUnsafeInstallDir = errors.New("unsafe install directory")

// VerifyChecksum checks file against its entry in a goreleaser checksums.txt.
func VerifyChecksum(checksums, file string) error {
	f, err := os.Open(checksums)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	name := filepath.Base(file)
	want := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimPrefix(sc.Text(), `\`) // GNU coreutils escaping marker
		if sum, n, ok := strings.Cut(line, "  "); ok && strings.TrimPrefix(n, "*") == name {
			want = sum
		}
	}
	if want == "" {
		return fmt.Errorf("%s has no checksum for %s", filepath.Base(checksums), name)
	}
	got, err := sha256File(file)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, want)
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Limits for an unpacked release: generous for squadrn (a ~10 MB binary and a few small files),
// small enough that a hostile archive cannot fill the disk.
const (
	maxEntryBytes = 256 << 20
	maxTotalBytes = 512 << 20
	maxEntries    = 1000
)

// Extract unpacks a release archive (.tar.gz, or .zip on Windows) into dst. Only regular files
// and directories are accepted, every path must stay inside dst, sizes are capped, and modes are
// reduced to 0755/0644.
func Extract(archive, dst string) error {
	if strings.HasSuffix(archive, ".zip") {
		return extractZip(archive, dst)
	}
	return extractTarGz(archive, dst)
}

// extractZip unpacks a Windows release with the same rules as extractTarGz: safe names, regular
// files and directories only, bounded sizes and entry count.
func extractZip(archive, dst string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(archive), err)
	}
	defer func() { _ = zr.Close() }()
	if len(zr.File) > maxEntries {
		return fmt.Errorf("%s: more than %d entries", filepath.Base(archive), maxEntries)
	}
	var total uint64
	seen := map[string]bool{}
	for _, zf := range zr.File {
		name, err := safeName(zf.Name)
		if err != nil {
			return err
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("%s: duplicate entry %s", filepath.Base(archive), zf.Name)
		}
		seen[key] = true
		target := filepath.Join(dst, name)
		mode := zf.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		case !mode.IsRegular():
			return fmt.Errorf("unsupported entry in archive: %s", zf.Name)
		}
		total += zf.UncompressedSize64
		if zf.UncompressedSize64 > maxEntryBytes || total > maxTotalBytes {
			return fmt.Errorf("%s: %s is too large", filepath.Base(archive), zf.Name)
		}
		perm := os.FileMode(0o644)
		if mode&0o111 != 0 || strings.EqualFold(filepath.Ext(name), ".exe") {
			perm = 0o755
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		// LimitReader guards against a header that understates the data that follows.
		err = fsx.WriteFile(target, io.LimitReader(rc, int64(zf.UncompressedSize64)), perm)
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(tarball, dst string) error {
	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(tarball), err)
	}
	tr := tar.NewReader(gz)
	var total int64
	for n := 0; ; n++ {
		if n >= maxEntries {
			return fmt.Errorf("%s: more than %d entries", filepath.Base(tarball), maxEntries)
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(tarball), err)
		}
		name, err := safeName(hdr.Name)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += hdr.Size
			if hdr.Size > maxEntryBytes || total > maxTotalBytes {
				return fmt.Errorf("%s: %s is too large", filepath.Base(tarball), hdr.Name)
			}
			mode := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}
			// LimitReader guards against a header that understates the data that follows.
			if err := fsx.WriteFile(target, io.LimitReader(tr, hdr.Size), mode); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported entry in archive: %s", hdr.Name)
		}
	}
}

// safeName validates an archive member name the same way on every platform: relative, no "..",
// no drive letters, colons (alternate data streams), backslashes or Windows reserved names.
func safeName(raw string) (string, error) {
	bad := func() (string, error) { return "", fmt.Errorf("unsafe path in archive: %s", raw) }
	if raw == "" || strings.HasPrefix(raw, "/") || strings.ContainsAny(raw, `\:`) {
		return bad()
	}
	for _, part := range strings.Split(strings.TrimSuffix(raw, "/"), "/") {
		upper := strings.ToUpper(strings.TrimRight(part, ". "))
		base, _, _ := strings.Cut(upper, ".")
		switch {
		case part == "..", part != "." && strings.TrimRight(part, ". ") != part:
			return bad()
		case base == "CON" || base == "PRN" || base == "AUX" || base == "NUL",
			len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9':
			return bad()
		}
	}
	return filepath.FromSlash(path.Clean(raw)), nil
}

type Layout struct {
	DataDir string
	BinDir  string
}

func DefaultLayout(home string) Layout {
	return Layout{DataDir: config.DirsFor(home, config.Slug).Data, BinDir: filepath.Join(home, ".local", "bin")}
}

func (l Layout) Binary() string { return filepath.Join(l.DataDir, "bin", BinaryName) }

// CleanOld removes executables earlier updates moved aside (Windows): the binary's and those of
// the command names given, never other programs' files in the shared bin dir.
func (l Layout) CleanOld(names []string) {
	fsx.CleanOld(filepath.Dir(l.Binary()), []string{BinaryName})
	files := make([]string, len(names))
	for i, n := range names {
		files[i] = executableName(n)
	}
	fsx.CleanOld(l.BinDir, files)
}

// Leftovers are the moved-aside executables of names in the bin dir, any age (uninstall).
func (l Layout) Leftovers(names []string) []string {
	files := make([]string, len(names))
	for i, n := range names {
		files[i] = executableName(n)
	}
	return fsx.Leftovers(l.BinDir, files, time.Now().Add(24*time.Hour))
}

// GitHooks is the directory of git-level guard shims.
func (l Layout) GitHooks() string { return githook.Dir(l.DataDir) }

// Marketplace is the local plugin marketplace dir, "" when not installed.
func (l Layout) Marketplace() string {
	if _, err := os.Stat(filepath.Join(l.DataDir, ".claude-plugin", "marketplace.json")); err != nil {
		return ""
	}
	return l.DataDir
}

// Incomplete reports whether the release installed here is not fully version: an install that
// stopped half way leaves a new binary next to an old plugin and VERSION. Without an installed
// binary there is nothing to repair.
func (l Layout) Incomplete(version string) bool {
	if !fsx.IsFile(l.Binary()) {
		return false
	}
	return l.Version() != strings.TrimPrefix(version, "v") || l.Marketplace() == "" ||
		!fsx.IsDir(filepath.Join(l.DataDir, "plugin"))
}

// IsInstalledCopy reports whether path is the installed binary or one of the command copies
// next to it in BinDir (Windows).
func (l Layout) IsInstalledCopy(path string) bool {
	return fsx.SameFile(path, l.Binary()) || filepath.Clean(filepath.Dir(path)) == filepath.Clean(l.BinDir)
}

// Install places an unpacked release (src holds squadrn, plugin/ and .claude-plugin/) into the
// data dir. The binary goes first: plugin hooks call it, so new plugin files never run against
// an old binary. The plugin version is set to version so Claude Code's plugin cache picks it up.
func (l Layout) Install(src, version string) error {
	for _, p := range []string{BinaryName, filepath.Join("plugin", ".claude-plugin", "plugin.json"), filepath.Join(".claude-plugin", "marketplace.json")} {
		if !fsx.IsFile(filepath.Join(src, p)) {
			return fmt.Errorf("not a squadrn release: missing %s", p)
		}
	}
	if err := os.MkdirAll(l.DataDir, 0o700); err != nil { // as fsx.WriteFile created it before
		return err
	}
	if err := fsx.ProtectDir(l.DataDir); err != nil {
		return err
	}
	if err := fsx.ReplaceExecutable(filepath.Join(src, BinaryName), l.Binary()); err != nil {
		return err
	}
	for _, dir := range []string{"plugin", ".claude-plugin"} {
		dst := filepath.Join(l.DataDir, dir)
		fsx.CleanStale(dst)
		if err := fsx.ReplaceDir(filepath.Join(src, dir), dst, 0o755); err != nil {
			return err
		}
	}
	if err := setPluginVersion(filepath.Join(l.DataDir, "plugin", ".claude-plugin", "plugin.json"), version); err != nil {
		return err
	}
	if err := fsx.WriteBytes(filepath.Join(l.DataDir, versionFile), []byte(strings.TrimPrefix(version, "v")+"\n"), 0o644); err != nil {
		return err
	}
	cfg, err := config.Path()
	if err != nil {
		return err
	}
	return githook.Install(l.GitHooks(), l.Binary(), cfg)
}

// versionFile records the installed version next to the binary.
const versionFile = "VERSION"

// Version is the installed release, "" when unknown.
func (l Layout) Version() string { return readVersion(l.DataDir) }

// Previous holds the release that was installed before the current one (for rollback).
func (l Layout) Previous() string { return filepath.Join(l.DataDir, "previous") }

// Snapshot copies the installed release into dst in the layout Install reads, so dst can be
// installed again later. It does nothing when no release is installed.
func (l Layout) Snapshot(dst string) error {
	if !fsx.IsFile(l.Binary()) {
		return nil
	}
	_ = os.RemoveAll(dst)
	if err := fsx.CopyFile(l.Binary(), filepath.Join(dst, BinaryName), 0o755, false); err != nil {
		return err
	}
	for _, dir := range []string{"plugin", ".claude-plugin"} {
		if err := fsx.CopyTree(filepath.Join(l.DataDir, dir), filepath.Join(dst, dir), 0o755, false); err != nil {
			return err
		}
	}
	return fsx.WriteBytes(filepath.Join(dst, versionFile), []byte(l.Version()+"\n"), 0o644)
}

// SnapshotVersion is the version recorded in a snapshot dir.
func SnapshotVersion(dir string) string { return readVersion(dir) }

func readVersion(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, versionFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

type LinkResult struct {
	Created  []string // links (re)pointed at the binary
	Removed  []string // links for names no longer wanted
	BackedUp []string // unrelated files moved aside as <name>.bak-<time>
}

func setPluginVersion(path, version string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal(b, &manifest); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	manifest["version"] = strings.TrimPrefix(version, "v")
	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteBytes(path, append(out, '\n'), 0o644)
}

// Compare orders release versions (v1.2.3, 1.2.3-rc.1) by semver 2.0 precedence: -1, 0 or 1.
// A pre-release sorts before its release; pre-release identifiers compare numerically when both
// are numbers (rc.9 < rc.10), numbers before words, and a shorter list first when the rest is
// equal. Build metadata (+…) is ignored. ok is false when either is not a version (e.g. "dev").
func Compare(a, b string) (order int, ok bool) {
	pa, prea, oka := parseVersion(a)
	pb, preb, okb := parseVersion(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range 3 {
		if c := cmp.Compare(pa[i], pb[i]); c != 0 {
			return c, true
		}
	}
	switch {
	case len(prea) == 0 && len(preb) == 0:
		return 0, true
	case len(prea) == 0:
		return 1, true
	case len(preb) == 0:
		return -1, true
	}
	for i := 0; i < len(prea) && i < len(preb); i++ {
		if c := comparePre(prea[i], preb[i]); c != 0 {
			return c, true
		}
	}
	return cmp.Compare(len(prea), len(preb)), true
}

// Valid reports whether v is a release version (v1.2.3, 1.2.3-rc.1); "dev" is not.
func Valid(v string) bool {
	_, _, ok := parseVersion(v)
	return ok
}

// Tag is v with the v prefix release tags carry (1.2.0 → v1.2.0). A version that is not a
// release (dev) is returned as it is.
func Tag(v string) string {
	if !Valid(v) {
		return v
	}
	return "v" + strings.TrimPrefix(v, "v")
}

// Prerelease reports whether v has a pre-release part (v1.2.0-rc.1).
func Prerelease(v string) bool {
	_, pre, ok := parseVersion(v)
	return ok && len(pre) > 0
}

func comparePre(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		return cmp.Compare(na, nb)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func parseVersion(v string) ([3]int, []string, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")
	v, pre, hasPre := strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, nil, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, nil, false
		}
		out[i] = n
	}
	if !hasPre {
		return out, nil, true
	}
	ids := strings.Split(pre, ".")
	for _, id := range ids {
		if id == "" {
			return out, nil, false
		}
	}
	return out, ids, true
}
