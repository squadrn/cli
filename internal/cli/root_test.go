package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squadrn/squadrn/internal/account"
)

func TestBannerAndPickerNeedATerminalNotColors(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	a := &app{cmd: "squadrn", store: account.Store{Home: home}, stderr: &stderr, ttyErr: true}
	acct, err := a.store.Get(account.Main)
	if err != nil {
		t.Fatal(err)
	}
	// NO_COLOR: the banner still shows, without escape codes.
	a.banner(acct)
	if got := stderr.String(); !strings.Contains(got, "account claude/main") || strings.Contains(got, "\033") {
		t.Fatalf("banner without colors: %q", got)
	}
	a.colorErr = true
	stderr.Reset()
	a.banner(acct)
	if !strings.Contains(stderr.String(), "\033[1mclaude/main\033[0m") {
		t.Fatalf("banner with colors: %q", stderr.String())
	}
	// Not a terminal: no banner, and the picker lists the accounts instead of drawing.
	a.ttyErr = false
	stderr.Reset()
	a.banner(acct)
	if stderr.Len() != 0 {
		t.Fatalf("banner off a terminal: %q", stderr.String())
	}
	err = a.pick()
	var ue usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "accounts: claude/main") {
		t.Fatalf("pick without a terminal: %v", err)
	}
}
