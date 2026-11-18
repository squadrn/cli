package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sprawling-agents/internal/release"
)

func TestVerifyRelease(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "t", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	asset := "sprawl_1.0.0_linux_amd64.tar.gz"
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(asset, "archive")
	out, _ := exec.Command("shasum", "-a", "256", filepath.Join(dir, asset)).Output()
	if len(out) == 0 {
		out, _ = exec.Command("sha256sum", filepath.Join(dir, asset)).Output()
	}
	write(release.ChecksumsName, strings.Fields(string(out))[0]+"  "+asset+"\n")
	if b, err := exec.Command("ssh-keygen", "-Y", "sign", "-q", "-f", key, "-n", release.SignatureNamespace, filepath.Join(dir, release.ChecksumsName)).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	fakeGH := filepath.Join(dir, "gh")
	write("gh", "#!/bin/sh\necho 'no attestations found' >&2\nexit 1\n")
	if err := os.Chmod(fakeGH, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := &app{cmd: "sprawl", stdout: &stdout, stderr: &stderr, signers: string(pub)}
	ctx := context.Background()
	if err := a.verifyRelease(ctx, fakeGH, dir, asset, "me/rel"); err != nil {
		t.Fatalf("signed release: %v", err)
	}
	if !strings.Contains(stdout.String(), "verified release signature") || stderr.Len() != 0 {
		t.Fatalf("stdout %q stderr %q", stdout.String(), stderr.String())
	}

	// A checksums file changed after signing is refused before the archive is looked at.
	write(release.ChecksumsName, "0000  "+asset+"\n")
	if err := a.verifyRelease(ctx, fakeGH, dir, asset, "me/rel"); err == nil || !strings.Contains(err.Error(), "not valid") {
		t.Fatalf("tampered checksums: %v", err)
	}
	// A release without a signature is refused by a build that has keys.
	_ = os.Remove(filepath.Join(dir, release.SignatureName))
	if err := a.verifyRelease(ctx, fakeGH, dir, asset, "me/rel"); err == nil || !strings.Contains(err.Error(), "has no signature") {
		t.Fatalf("unsigned release: %v", err)
	}
	// A build without keys falls back to the checksum, and says so.
	a.signers = ""
	if err := a.verifyRelease(ctx, fakeGH, dir, asset, "me/rel"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
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
