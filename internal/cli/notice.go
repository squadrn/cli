package cli

import (
	"os"
	"os/exec"
	"slices"
	"syscall"
	"time"

	"sprawling-agents/internal/update"
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
	if !a.interactive || !a.colorErr || a.cfgErr != nil || !a.cfg.UpdateCheckEnabled() ||
		update.Disabled(os.Getenv) || slices.Contains(quietCommands, r.sub) || slices.Contains(r.args, "--json") {
		return
	}
	path := a.updateStatePath()
	s := update.Load(path)
	now := a.now()
	if latest, ok := update.Available(Version, s); ok && now.Sub(s.NotifiedAt) >= noticeEvery {
		a.errf("%s\n", a.paint(true, "1;36", "✦ sprawl "+latest+" is available")+
			a.paint(true, "2", " (you have v"+trimV(Version)+") · "+a.cmd+" self-update · what's new: "+a.cmd+" self-update --check"))
		s.NotifiedAt = now
		update.Save(path, s)
	}
	if Repo != "" && update.Due(s, now) {
		s.CheckedAt = now // claim the slot so parallel invocations do not all start a check
		update.Save(path, s)
		if a.backgroundCheck != nil {
			a.backgroundCheck()
		} else {
			startBackgroundCheck()
		}
	}
}

func trimV(v string) string {
	if len(v) > 0 && v[0] == 'v' {
		return v[1:]
	}
	return v
}

// startBackgroundCheck runs `sprawl self-update --background` detached: in its own session,
// with no terminal, so it outlives this process (and an exec into the agent) without waiting.
func startBackgroundCheck() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "self-update", "--background")
	cmd.Env = append(os.Environ(), "SPRAWL_NO_UPDATE_CHECK=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}
