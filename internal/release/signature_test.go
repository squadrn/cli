package release

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sshKey creates a throwaway ed25519 key with ssh-keygen and returns its paths and public line.
func sshKey(t *testing.T, dir, name string) (key, pub string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}
	key = filepath.Join(dir, name)
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "test", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	b, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return key, string(b)
}

func sshSign(t *testing.T, key, namespace, file string) []byte {
	t.Helper()
	_ = os.Remove(file + ".sig")
	if out, err := exec.Command("ssh-keygen", "-Y", "sign", "-q", "-f", key, "-n", namespace, file).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -Y sign: %v\n%s", err, out)
	}
	b, err := os.ReadFile(file + ".sig")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVerifySignature(t *testing.T) {
	dir := t.TempDir()
	key, pub := sshKey(t, dir, "k")
	_, otherPub := sshKey(t, dir, "other")
	msg := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(msg, []byte("abc  squadrn_1.0.0_linux_amd64.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(msg)
	sig := sshSign(t, key, signatureNamespace, msg)

	if err := VerifySignature(body, sig, "# comment\n\n"+pub); err != nil {
		t.Fatalf("valid signature: %v", err)
	}
	if err := VerifySignature(body, sig, otherPub+pub); err != nil {
		t.Fatalf("rotation (two keys): %v", err)
	}
	if err := VerifySignature(append(body, 'x'), sig, pub); !errors.Is(err, errBadSignature) {
		t.Errorf("tampered message: %v", err)
	}
	if err := VerifySignature(body, sig, otherPub); !errors.Is(err, errBadSignature) || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("unknown key: %v", err)
	}
	if err := VerifySignature(body, sig, ""); !errors.Is(err, ErrNoSigners) {
		t.Errorf("no signers: %v", err)
	}
	if err := VerifySignature(body, []byte("junk"), pub); !errors.Is(err, errBadSignature) {
		t.Errorf("junk: %v", err)
	}
	other := sshSign(t, key, "git", msg)
	if err := VerifySignature(body, other, pub); !errors.Is(err, errBadSignature) || !strings.Contains(err.Error(), "namespace") {
		t.Errorf("wrong namespace: %v", err)
	}
	for _, bad := range []string{"ssh-rsa AAAA x", "ssh-ed25519 !!!", "ssh-ed25519"} {
		if err := VerifySignature(body, sig, bad); err == nil {
			t.Errorf("signers %q accepted", bad)
		}
	}
	// ssh-keygen agrees with the signers file format (allowed_signers uses the same key fields).
	if err := os.WriteFile(msg+".sig", sig, 0o600); err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(dir, "allowed")
	if err := os.WriteFile(allowed, []byte("squadrn "+pub), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowed, "-I", "squadrn", "-n", signatureNamespace, "-s", msg+".sig")
	cmd.Stdin = strings.NewReader(string(body))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -Y verify: %v\n%s", err, out)
	}
}

func TestEmbeddedSignersParse(t *testing.T) {
	keys, err := parseSigners(signers)
	if err != nil {
		t.Fatal(err)
	}
	if (Signers() != "") != (len(keys) > 0) {
		t.Fatalf("Signers = %q with %d keys", Signers(), len(keys))
	}
}
