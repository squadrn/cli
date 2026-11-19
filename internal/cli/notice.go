package cli

import (
	"os"
	"os/exec"
	"slices"
	"time"

	"github.com/squadrn/squadrn/internal/release"
	"github.com/squadrn/squadrn/internal/update"
)

// noticeEvery limits how often the same update notice is shown.
const noticeEvery = 6 * time.Hour

// quietCommands never show the notice or start a check: they are the update path itself, or
// machine-facing.
var quietCommands = []string{"self-update", "self-install", "self-uninstall", "completion", "__complete", "__completeNoDesc"}

// updateNotice mentions a newer release on stderr, like Claude Code does: one line, never
// blocking, never forcing. It also starts the daily background check when one is due. Only
// for people at a terminal; never with --json, in CI or when turned off.
func (a *app) updateNotice(r route) {
	if !a.interactive || !a.ttyErr || a.cfgErr != nil || !a.cfg.UpdateCheckEnabled() ||
		update.Disabled(os.Getenv) || slices.Contains(quietCommands, r.sub) || slices.Contains(r.args, "--json") {
		return
	}
	path := a.updateStatePath()
	s := update.Load(path)
	now := a.now()
	if latest, ok := update.Available(Version, s, a.cfg.Channel()); ok && now.Sub(s.NotifiedAt) >= noticeEvery {
		a.errf("%s\n", a.paint(a.colorErr, "1;36", "✦ squadrn "+latest+" is available")+
			a.paint(a.colorErr, "2", " (you have "+release.Tag(Version)+") · "+a.cmd+" self-update · what's new: "+a.cmd+" self-update --check"))
		s.NotifiedAt = now
		update.Save(path, s)
	}
	// Development builds (not a release version) never check in the background.
	if Repo != "" && release.Valid(Version) && update.Due(s, now, a.cfg.Channel()) {
		s.CheckedAt = now // claim the slot so parallel invocations do not all start a check
		update.Save(path, s)
		if a.backgroundCheck != nil {
			a.backgroundCheck()
		} else {
			startBackgroundCheck()
		}
	}
}

// startBackgroundCheck runs `squadrn self-update --background` detached: in its own session,
// with no terminal, so it outlives this process (and an exec into the agent) without waiting.
func startBackgroundCheck() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "self-update", "--background")
	cmd.Env = append(os.Environ(), "SQUADRN_NO_UPDATE_CHECK=1")
	if startDetached(cmd) == nil {
		_ = cmd.Process.Release()
	}
}
