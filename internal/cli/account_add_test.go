package cli

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/squadrn/squadrn/internal/provider"
)

func TestAskProfileAndYes(t *testing.T) {
	var errOut bytes.Buffer
	a := &app{cmd: "srn", stdout: io.Discard, stderr: &errOut}
	for input, want := range map[string]string{
		"\n": provider.DefaultProfile, "strict\n": "strict", "mi\n": "minimal", "bogus\nst\n": "standard",
		"": provider.DefaultProfile,
	} {
		got, err := a.askProfile(bufio.NewReader(strings.NewReader(input)))
		if err != nil || got != want {
			t.Errorf("askProfile(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := a.askProfile(bufio.NewReader(strings.NewReader("bogus"))); err == nil {
		t.Error("an unknown profile at EOF was accepted")
	}
	if !strings.Contains(errOut.String(), "profile        minimal / standard / strict [standard]: ") {
		t.Errorf("prompt = %q", errOut.String())
	}
	for input, want := range map[string]bool{"\n": true, "y\n": true, "n\n": false, "No\n": false, "": true} {
		if got, err := a.askYes(bufio.NewReader(strings.NewReader(input)), "log in now?", ""); err != nil || got != want {
			t.Errorf("askYes(%q) = %v, %v", input, got, err)
		}
	}
}

func TestProfileDescriptions(t *testing.T) {
	if got := profileDesc("standard", []string{"privacy"}); got != "standard (secrets denied, push guard, 30-day history) + privacy" {
		t.Errorf("profileDesc = %q", got)
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
