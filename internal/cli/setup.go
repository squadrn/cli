package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/provider"
)

func (a *app) setupCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Guided first run: agent CLIs, default profile, existing accounts, usage tracking, doctor",
		Long: `Guided first run, safe to repeat: finds the agent CLIs on PATH, asks for the default provider
and settings profile, offers to adopt config dirs named like accounts (~/.claude-<name>), offers
usage tracking for the accounts that do not record it yet, then runs doctor. Every step can be
skipped; without a terminal nothing is changed unless --yes takes the defaults.`,
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error { return a.setup(yes) },
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "take the defaults without asking (adopts nothing)")
	return cmd
}

// detected is an agent CLI found on PATH.
type detected struct {
	spec    provider.Spec
	path    string
	version string
}

// detectCLIs looks for every provider's CLI on PATH, asking each for its version in parallel.
func (a *app) detectCLIs() []detected {
	specs := provider.All()
	out := make([]detected, len(specs))
	done := make(chan struct{}, len(specs))
	for i, spec := range specs {
		out[i].spec = spec
		go func() {
			defer func() { done <- struct{}{} }()
			p, err := exec.LookPath(spec.Binary)
			if err != nil {
				return
			}
			out[i].path = p
			if ag, err := a.agent(spec); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
				b, _ := ag.Output(ctx, "", "--version")
				cancel()
				out[i].version = provider.ParseVersion(b)
			}
		}()
	}
	for range specs {
		<-done
	}
	return out
}

func (a *app) cliLine(d detected) string {
	if d.path == "" {
		return fmt.Sprintf("%s not found (install: %s)", d.spec.Name, d.spec.InstallHint)
	}
	name := d.spec.Name
	if d.version != "" {
		name += " " + d.version
	}
	return fmt.Sprintf("%s (%s)", name, a.tilde(d.path))
}

// setup is the guided first run. ask is false without a terminal: then only --yes changes
// anything, and it takes the defaults.
func (a *app) setup(yes bool) error {
	if a.cfgErr != nil {
		return a.cfgErr
	}
	ask := a.interactive && !yes
	if !ask && !yes {
		a.summary()
		return nil
	}
	in := bufio.NewReader(a.stdin)
	ok, mark := a.paint(a.colorOut, "32", "✓"), a.paint(a.colorOut, "2", "–")

	a.out("1. agent CLIs\n")
	clis := a.detectCLIs()
	var found []detected
	for _, d := range clis {
		if d.path != "" {
			found = append(found, d)
			a.out("   %s %s\n", ok, a.cliLine(d))
		} else {
			a.out("   %s %s\n", mark, a.cliLine(d))
		}
	}
	if len(found) == 0 {
		a.out("   no agent CLI found; install one, then run: %s setup\n", a.cmd)
		return nil
	}

	a.out("2. defaults\n")
	cfg := a.cfg
	if len(found) > 1 && ask {
		ids := make([]string, len(found))
		for i, d := range found {
			ids[i] = d.spec.ID
		}
		answer, err := a.askLine(in, "default provider", strings.Join(ids, " / "), a.store.DefaultProvider)
		if err != nil {
			return err
		}
		if _, known := provider.Get(answer); known {
			cfg.DefaultProvider = answer
		}
	}
	profile := cfg.ProfileName()
	if ask {
		var err error
		if profile, err = a.askProfile(in, profile); err != nil {
			return err
		}
	} else {
		a.out("   %-14s %s\n", "profile", profileDesc(profile, nil))
	}
	if profile != cfg.ProfileName() || cfg.DefaultProvider != a.cfg.DefaultProvider {
		cfg.DefaultProfile = profile
		path, err := config.Path()
		if err != nil {
			return err
		}
		if err := config.Save(path, cfg); err != nil {
			return err
		}
		a.cfg = cfg
		a.store.DefaultProvider = cfg.DefaultProvider
		a.out("   %s new accounts get the %s profile\n", ok, profile)
	}

	a.out("3. existing accounts\n")
	accts, err := a.store.List()
	if err != nil {
		return err
	}
	labels := make([]string, len(accts))
	for i, acct := range accts {
		labels[i] = acct.Label()
	}
	a.out("   %s\n", strings.Join(labels, ", "))
	un, _ := a.store.Unmanaged()
	if len(un) == 0 {
		a.out("   %s nothing to adopt\n", ok)
	}
	for _, acct := range un {
		adopt := false
		if ask {
			a.errf("   adopt %s as %s? (another tool may own it) [y/N] ", a.tilde(acct.Dir), acct.Label())
			line, err := in.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			adopt = strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y")
		}
		if !adopt {
			a.out("   %s skipped %s (later: %s account adopt %s)\n", mark, a.tilde(acct.Dir), a.cmd, acct.Label())
			continue
		}
		if _, err := a.store.Adopt(acct.Label()); err != nil {
			return err
		}
		a.out("   adopted %s\n", acct.Label())
		if err := a.accountAdd(acct.Label(), addOptions{yes: true, usage: new(bool), login: new(bool)}); err != nil {
			a.warnf("%s: %v", acct.Label(), err)
		}
	}

	a.out("4. usage tracking\n")
	accts, _ = a.store.List()
	var untracked []account.Account
	for _, acct := range accts {
		if statusLineOf(acct.Spec) != nil && !a.usageTracking(acct) {
			untracked = append(untracked, acct)
		}
	}
	if len(untracked) == 0 {
		a.out("   %s on for every account that reports usage\n", ok)
	} else {
		names := make([]string, len(untracked))
		for i, acct := range untracked {
			names[i] = acct.Label()
		}
		track := yes
		if ask {
			if track, err = a.askYes(in, "track", "usage limits for "+strings.Join(names, ", ")+"?"); err != nil {
				return err
			}
		}
		if !track {
			a.out("   %s skipped (later: %s usage setup --all)\n", mark, a.cmd)
		}
		for _, acct := range untracked {
			if !track {
				break
			}
			if err := a.enableUsage(acct); err != nil {
				a.warnf("usage tracking for %s: %v", acct.Label(), err)
				continue
			}
			a.out("   %s usage tracking on: %s\n", ok, acct.Label())
		}
	}

	a.out("5. doctor\n")
	if err := a.runDoctor(""); err != nil && !errors.Is(err, errQuiet) {
		return err
	}
	a.nextSteps()
	return nil
}

// askLine asks a free-form question with a default on one plan line.
func (a *app) askLine(in *bufio.Reader, label, choices, def string) (string, error) {
	a.errf("   %-14s %s [%s]: ", label, choices, def)
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if answer := strings.TrimSpace(line); answer != "" {
		return answer, nil
	}
	return def, nil
}

// summary is what setup (and a first install without a terminal) prints instead of asking:
// the agent CLIs, the accounts and what to run next.
func (a *app) summary() {
	var clis []string
	for _, d := range a.detectCLIs() {
		clis = append(clis, a.cliLine(d))
	}
	a.out("agent CLIs: %s\n", strings.Join(clis, " · "))
	accts, _ := a.store.List()
	labels := make([]string, len(accts))
	for i, acct := range accts {
		labels[i] = acct.Label()
	}
	line := "accounts: " + strings.Join(labels, ", ")
	if un, _ := a.store.Unmanaged(); len(un) > 0 {
		dirs := make([]string, len(un))
		for i, acct := range un {
			dirs[i] = a.tilde(acct.Dir)
		}
		line += " (to adopt: " + strings.Join(dirs, ", ") + ")"
	}
	a.out("%s\n", line)
	a.out("next: %s setup (guided: profile, adopt, usage tracking, doctor)\n", a.cmd)
}

func (a *app) nextSteps() {
	a.out("next: %[1]s account add <name> (a new account) · %[1]s <account> (start a session) · %[1]s grid (side by side)\n", a.cmd)
}
