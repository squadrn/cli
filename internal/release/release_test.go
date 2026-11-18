package release

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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
