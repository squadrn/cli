package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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

func writeZip(t *testing.T, path string, files map[string]string, modes map[string]os.FileMode) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := &zip.FileHeader{Name: n, Method: zip.Deflate}
		if m, ok := modes[n]; ok {
			h.SetMode(m)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[n])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractZip(t *testing.T) {
	dir := t.TempDir()
	z := filepath.Join(dir, "r.zip")
	writeZip(t, z, map[string]string{"sprawl.exe": "bin", "plugin/hooks/hooks.json": "{}", "plugin/": ""},
		map[string]os.FileMode{"plugin/": os.ModeDir | 0o755})
	dst := filepath.Join(dir, "out")
	if err := Extract(z, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "plugin", "hooks", "hooks.json")); string(b) != "{}" {
		t.Fatalf("hooks.json = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "sprawl.exe")); string(b) != "bin" {
		t.Fatalf("sprawl.exe = %q", b)
	}
}

func TestExtractZipRejectsUnsafeEntries(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"traversal":    {"../escape": "x"},
		"backslash":    {`a\..\..\x`: "x"},
		"drive":        {"C:/x": "x"},
		"stream":       {"a.txt:hidden": "x"},
		"reserved":     {"CON": "x"},
		"reserved-ext": {"plugin/nul.txt": "x"},
		"trailing dot": {"a.": "x"},
		"duplicate":    {"A.txt": "x", "a.txt": "y"},
		"rooted":       {"/etc/x": "x"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			z := filepath.Join(dir, "r.zip")
			writeZip(t, z, files, nil)
			if err := Extract(z, filepath.Join(dir, "out")); err == nil {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
	dir := t.TempDir()
	z := filepath.Join(dir, "link.zip")
	writeZip(t, z, map[string]string{"link": "/etc/passwd"}, map[string]os.FileMode{"link": os.ModeSymlink | 0o777})
	if err := Extract(z, filepath.Join(dir, "out")); err == nil {
		t.Fatal("symlink accepted")
	}
	many := map[string]string{}
	for i := range maxEntries + 1 {
		many[fmt.Sprintf("f%04d", i)] = ""
	}
	z = filepath.Join(dir, "many.zip")
	writeZip(t, z, many, nil)
	if err := Extract(z, filepath.Join(dir, "out2")); err == nil {
		t.Fatal("entry cap not enforced")
	}
}

func TestSafeName(t *testing.T) {
	for _, ok := range []string{"sprawl", "plugin/hooks/hooks.json", ".claude-plugin/marketplace.json", "./README.md", "dir/"} {
		if _, err := safeName(ok); err != nil {
			t.Errorf("safeName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "..", "a/../../b", "/x", `x\y`, "C:x", "COM1", "lpt9.txt", "aux", "x ", "x."} {
		if _, err := safeName(bad); err == nil {
			t.Errorf("safeName(%q) accepted", bad)
		}
	}
}
