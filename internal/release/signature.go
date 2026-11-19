package release

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha512"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// SignatureNamespace binds release signatures to their purpose: a key used here cannot have its
// signatures replayed from another context (ssh-keygen -Y sign -n squadrn-release).
const SignatureNamespace = "squadrn-release"

// SignatureName is the detached signature of ChecksumsName published with every release.
const SignatureName = ChecksumsName + ".sig"

// signers holds the public keys allowed to sign releases, one "ssh-ed25519 <base64> [comment]"
// per line (several during a key rotation). Empty means release signing is not set up yet.
//
//go:embed signers.pub
var signers string

// Signers returns the embedded release signing keys.
func Signers() string { return signers }

// HasSigners reports whether this build carries at least one valid release signing key.
func HasSigners() bool {
	keys, err := parseSigners(signers)
	return err == nil && len(keys) > 0
}

var (
	ErrNoSigners    = errors.New("no release signing key is configured in this build")
	ErrBadSignature = errors.New("release signature is not valid")
)

// VerifySignature checks an SSH signature (ssh-keygen -Y sign, armored SSHSIG) of message made
// in SignatureNamespace by one of the ed25519 keys in allowed.
func VerifySignature(message, armored []byte, allowed string) error {
	keys, err := parseSigners(allowed)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return ErrNoSigners
	}
	block, _ := pem.Decode(armored)
	if block == nil || block.Type != "SSH SIGNATURE" {
		return fmt.Errorf("%w: not an SSH signature", ErrBadSignature)
	}
	r := sshReader{b: block.Bytes}
	if magic := r.raw(6); string(magic) != "SSHSIG" {
		return fmt.Errorf("%w: bad magic", ErrBadSignature)
	}
	if v := r.uint32(); v != 1 {
		return fmt.Errorf("%w: unsupported version %d", ErrBadSignature, v)
	}
	pubBlob := r.str()
	namespace, reserved, hashAlg := r.str(), r.str(), r.str()
	sigBlob := r.str()
	if r.err != nil || len(r.b) != 0 {
		return fmt.Errorf("%w: malformed", ErrBadSignature)
	}
	if string(namespace) != SignatureNamespace {
		return fmt.Errorf("%w: namespace %q", ErrBadSignature, namespace)
	}
	if string(hashAlg) != "sha512" {
		return fmt.Errorf("%w: hash %q", ErrBadSignature, hashAlg)
	}
	pr := sshReader{b: pubBlob}
	if string(pr.str()) != "ssh-ed25519" {
		return fmt.Errorf("%w: only ed25519 keys are accepted", ErrBadSignature)
	}
	pub := pr.str()
	sr := sshReader{b: sigBlob}
	algo, sig := sr.str(), sr.str()
	if pr.err != nil || sr.err != nil || string(algo) != "ssh-ed25519" || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: malformed key or signature", ErrBadSignature)
	}
	trusted := false
	for _, k := range keys {
		if bytes.Equal(k, pub) {
			trusted = true
		}
	}
	if !trusted {
		return fmt.Errorf("%w: signed by an unknown key", ErrBadSignature)
	}
	digest := sha512.Sum512(message)
	var signed bytes.Buffer
	signed.WriteString("SSHSIG")
	for _, f := range [][]byte{namespace, reserved, hashAlg, digest[:]} {
		writeString(&signed, f)
	}
	if !ed25519.Verify(pub, signed.Bytes(), sig) {
		return ErrBadSignature
	}
	return nil
}

// parseSigners reads "ssh-ed25519 <base64> [comment]" lines; blank lines and # comments are skipped.
func parseSigners(s string) ([][]byte, error) {
	var keys [][]byte
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if len(f) < 2 || f[0] != "ssh-ed25519" {
			return nil, fmt.Errorf("signing keys: unsupported line %q", sc.Text())
		}
		blob, err := base64.StdEncoding.DecodeString(f[1])
		if err != nil {
			return nil, fmt.Errorf("signing keys: %w", err)
		}
		r := sshReader{b: blob}
		if string(r.str()) != "ssh-ed25519" {
			return nil, errors.New("signing keys: key type mismatch")
		}
		pub := r.str()
		if r.err != nil || len(pub) != ed25519.PublicKeySize {
			return nil, errors.New("signing keys: malformed ed25519 key")
		}
		keys = append(keys, pub)
	}
	return keys, sc.Err()
}

// sshReader decodes the SSH wire format (RFC 4251): uint32 lengths followed by bytes.
type sshReader struct {
	b   []byte
	err error
}

func (r *sshReader) raw(n int) []byte {
	if r.err != nil || len(r.b) < n {
		r.err = errors.New("short")
		return nil
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func (r *sshReader) uint32() uint32 {
	b := r.raw(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

func (r *sshReader) str() []byte {
	n := r.uint32()
	if r.err != nil || uint64(n) > uint64(len(r.b)) {
		r.err = errors.New("short")
		return nil
	}
	return r.raw(int(n))
}

func writeString(w *bytes.Buffer, b []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(b)))
	w.Write(n[:])
	w.Write(b)
}
