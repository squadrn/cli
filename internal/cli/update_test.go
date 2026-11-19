package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/squadrn/squadrn/internal/release"
)

func TestVerifyRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "t", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	asset := "squadrn_1.0.0_linux_amd64.tar.gz"
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(asset, "archive")
	sum := sha256.Sum256([]byte("archive"))
	write(release.ChecksumsName, hex.EncodeToString(sum[:])+"  "+asset+"\n")
	if b, err := exec.Command("ssh-keygen", "-Y", "sign", "-q", "-f", key, "-n", "squadrn-release", filepath.Join(dir, release.ChecksumsName)).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	var stdout, stderr bytes.Buffer
	a := &app{cmd: "squadrn", stdout: &stdout, stderr: &stderr, signers: string(pub)}
	if err := a.verifyRelease(dir, asset); err != nil {
		t.Fatalf("signed release: %v", err)
	}
	if !strings.Contains(stdout.String(), "verified release signature") || stderr.Len() != 0 {
		t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
	}

	// A checksums file changed after signing is refused before the archive is looked at.
	write(release.ChecksumsName, "0000  "+asset+"\n")
	if err := a.verifyRelease(dir, asset); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("tampered checksums: %v", err)
	}
	// A release without a signature is refused by a build that has keys.
	_ = os.Remove(filepath.Join(dir, release.SignatureName))
	if err := a.verifyRelease(dir, asset); err == nil || !strings.Contains(err.Error(), "has no signature") {
		t.Fatalf("unsigned release: %v", err)
	}
	// A build without keys falls back to the checksum, and says so.
	a.signers = ""
	if err := a.verifyRelease(dir, asset); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("no keys, bad checksum: %v", err)
	}
	if !strings.Contains(stderr.String(), "no release signing key") {
		t.Fatalf("stderr %q", stderr.String())
	}
}

func TestReleaseNotes(t *testing.T) {
	got := releaseNotes("a\r\nb\nc\n", 2)
	if got != "  a\n  b\n  …" {
		t.Fatalf("releaseNotes = %q", got)
	}
}
