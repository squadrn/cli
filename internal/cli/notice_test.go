package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/squadrn/squadrn-cli/internal/account"
	"github.com/squadrn/squadrn-cli/internal/config"
	"github.com/squadrn/squadrn-cli/internal/update"
)

func TestUpdateNotice(t *testing.T) {
	t.Setenv("SQUADRN_NO_UPDATE_CHECK", "")
	t.Setenv("CI", "")
	oldRepo, oldVersion := Repo, Version
	Repo, Version = "me/rel", "1.0.0"
	defer func() { Repo, Version = oldRepo, oldVersion }()

	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	var stderr bytes.Buffer
	started := 0
	a := &app{
		cmd: "squadrn", store: account.Store{Home: t.TempDir()}, stderr: &stderr,
		interactive: true, colorErr: true, ttyErr: true, now: func() time.Time { return now },
		backgroundCheck: func() { started++ },
	}
	// Nothing known yet: no notice, one background check, not repeated within the interval.
	a.updateNotice(route{})
	a.updateNotice(route{})
	if stderr.Len() != 0 || started != 1 {
		t.Fatalf("first run: %q, %d checks", stderr.String(), started)
	}
	update.Save(a.updateStatePath(), update.State{CheckedAt: now, Latest: "v1.1.0", Channel: config.Stable})
	a.updateNotice(route{})
	if !strings.Contains(stderr.String(), "squadrn v1.1.0 is available") || !strings.Contains(stderr.String(), "you have v1.0.0") {
		t.Fatalf("notice: %q", stderr.String())
	}
	// Shown at most every noticeEvery.
	stderr.Reset()
	a.updateNotice(route{})
	if stderr.Len() != 0 {
		t.Fatalf("repeated notice: %q", stderr.String())
	}
	now = now.Add(7 * time.Hour)
	a.updateNotice(route{sub: "ps"})
	if !strings.Contains(stderr.String(), "available") {
		t.Fatal("notice not shown again after the interval")
	}
	// Quiet for machine-facing commands, --json, opt-outs and non-terminals.
	now = now.Add(7 * time.Hour)
	stderr.Reset()
	for _, r := range []route{{sub: "self-update"}, {sub: "completion"}, {sub: "ps", args: []string{"--json"}}} {
		a.updateNotice(r)
	}
	t.Setenv("SQUADRN_NO_UPDATE_CHECK", "1")
	a.updateNotice(route{})
	t.Setenv("SQUADRN_NO_UPDATE_CHECK", "")
	off := false
	a.cfg.UpdateCheck = &off
	a.updateNotice(route{})
	a.cfg.UpdateCheck = nil
	a.interactive = false
	a.updateNotice(route{})
	if stderr.Len() != 0 {
		t.Fatalf("notice where it must be quiet: %q", stderr.String())
	}
	// Development builds are never nagged.
	a.interactive, Version = true, "dev"
	a.updateNotice(route{})
	if stderr.Len() != 0 {
		t.Fatalf("dev build nagged: %q", stderr.String())
	}
}
