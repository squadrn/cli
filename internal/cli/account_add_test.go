package cli

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/squadrn/squadrn-cli/internal/account"
	"github.com/squadrn/squadrn-cli/internal/provider"
)

func TestAskProfileAndYes(t *testing.T) {
	var errOut bytes.Buffer
	a := &app{cmd: "srn", stdout: io.Discard, stderr: &errOut}
	for input, want := range map[string]string{
		"\n": provider.DefaultProfile, "strict\n": "strict", "mi\n": "minimal", "bogus\nst\n": "standard",
		"": provider.DefaultProfile,
	} {
		got, err := a.askProfile(bufio.NewReader(strings.NewReader(input)), provider.Claude, provider.DefaultProfile)
		if err != nil || got != want {
			t.Errorf("askProfile(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := a.askProfile(bufio.NewReader(strings.NewReader("bogus")), provider.Claude, provider.DefaultProfile); err == nil {
		t.Error("an unknown profile at EOF was accepted")
	}
	for _, want := range []string{
		"  profiles       minimal   secrets denied\n",
		"                 standard  secrets denied, push guard, 30-day history\n",
		"profile        minimal / standard / strict [standard]: ",
	} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("prompt %q lacks %q", errOut.String(), want)
		}
	}
	errOut.Reset()
	if got, err := a.askProfile(bufio.NewReader(strings.NewReader("\n")), provider.Codex, provider.DefaultProfile); err != nil || got != provider.DefaultProfile {
		t.Errorf("codex askProfile = %q, %v", got, err)
	}
	if !strings.Contains(errOut.String(), "                 strict    read-only workspace; use standard for daily work\n") {
		t.Errorf("codex prompt = %q", errOut.String())
	}
	for input, want := range map[string]bool{"\n": true, "y\n": true, "n\n": false, "No\n": false, "": true} {
		if got, err := a.askYes(bufio.NewReader(strings.NewReader(input)), "log in now?", ""); err != nil || got != want {
			t.Errorf("askYes(%q) = %v, %v", input, got, err)
		}
	}
	for input, want := range map[string]bool{"\n": false, "y\n": true, "Yes\n": true, "n\n": false, "": false} {
		if got, err := a.askNo(bufio.NewReader(strings.NewReader(input)), "relink", "move x?"); err != nil || got != want {
			t.Errorf("askNo(%q) = %v, %v", input, got, err)
		}
	}
	if !strings.Contains(errOut.String(), "  relink         move x? [y/N] ") {
		t.Errorf("askNo prompt = %q", errOut.String())
	}
}

func TestProfileDescriptions(t *testing.T) {
	if got := profileDesc(provider.Claude, "standard", []string{"privacy"}); got != "standard (secrets denied, push guard, 30-day history) + privacy" {
		t.Errorf("profileDesc = %q", got)
	}
	if got := profileDesc(provider.Codex, "strict", nil); got != "strict (read-only workspace; use standard for daily work)" {
		t.Errorf("codex profileDesc = %q", got)
	}
	if got := profileName("strict", nil); got != "strict profile" {
		t.Errorf("profileName = %q", got)
	}
	if got := loginName(provider.AuthStatus{Email: "me@x", SubscriptionType: "max"}); got != "me@x (Max)" {
		t.Errorf("loginName = %q", got)
	}
	if got := loginName(provider.AuthStatus{}); got != "this account" {
		t.Errorf("loginName = %q", got)
	}
	if !strings.Contains(optionList(), "privacy") {
		t.Errorf("optionList = %q", optionList())
	}
}

func TestGuardLineTrustsAFreshInstall(t *testing.T) {
	a := &app{}
	// A nil agent proves the CLI is not asked for the plugin state right after installing it.
	if got := a.guardLine(account.Account{Spec: provider.Claude}, nil, true); got != "pre-tool hook + git pre-push" {
		t.Errorf("guardLine = %q", got)
	}
	if got := a.guardLine(account.Account{Spec: provider.Claude}, nil, false); got != "" {
		t.Errorf("guardLine without a guard installer = %q", got)
	}
}
