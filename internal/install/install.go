// Package install checks command names and installs or removes the squadrn plugin in every
// account. It returns what it did; cli reports it.
package install

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/provider"
	"github.com/squadrn/squadrn/internal/release"
)

var (
	ErrNameTaken     = errors.New("command name is taken")
	ErrDuplicateName = errors.New("command name given twice")
)

// Names is the set of names a config installs (accepted earlier, maybe forced).
func Names(cfg config.Config) map[string]bool {
	names := map[string]bool{}
	if cfg.Command != "" {
		names[cfg.Command] = true
	}
	for _, n := range cfg.Aliases {
		names[n] = true
	}
	return names
}

// NameCheck validates the command and aliases and refuses new names another program on PATH
// already answers to. Names accepted before are not re-checked; names in the bin dir are
// handled by LinkCommands.
type NameCheck struct {
	Layout   release.Layout
	Accepted map[string]bool
	Force    bool
	LookPath func(string) (string, error)
	// Self is the running binary; a name resolving to it (or to the installed one) is no clash.
	Self string
}

func (n NameCheck) Check(cfg config.Config) error {
	seen := map[string]bool{}
	for _, name := range append([]string{cfg.Command}, cfg.Aliases...) {
		if err := n.Valid(name); err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("%w: %q", ErrDuplicateName, name)
		}
		seen[name] = true
		if p := n.Clash(name); p != "" && !n.Force {
			return fmt.Errorf("%w: %q is already a command on PATH (%s); pick another name or pass --force", ErrNameTaken, name, p)
		}
	}
	return nil
}

// Valid checks the form of name; a name not accepted before must also be portable
// (config.CheckPortable).
func (n NameCheck) Valid(name string) error {
	if err := config.ValidateCommand(name); err != nil {
		return err
	}
	if n.Accepted[name] {
		return nil
	}
	return config.CheckPortable(name)
}

// Clash returns the path of another program that already answers to name, "" when none does
// (or the name was accepted before, or it is squadrn itself).
func (n NameCheck) Clash(name string) string {
	lookPath := n.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	p, err := lookPath(name)
	if err != nil || filepath.Dir(p) == n.Layout.BinDir || n.Accepted[name] || n.isSelf(p) {
		return ""
	}
	return p
}

func (n NameCheck) isSelf(path string) bool {
	for _, own := range []string{n.Layout.Binary(), n.Self} {
		if own != "" && fsx.SameFile(path, own) {
			return true
		}
	}
	return false
}

// PluginAction is what happened to the squadrn plugin in one account.
type PluginAction string

const (
	PluginInstalled PluginAction = "installed"
	PluginUpdated   PluginAction = "updated"
	PluginFailed    PluginAction = "failed"
)

type PluginResult struct {
	Account account.Account
	Action  PluginAction
	Err     error
}

// Plugins installs or refreshes the plugin in every account whose provider supports it.
func Plugins(ctx context.Context, accts []account.Account, store account.Store, r provider.Runner, marketplace string) []PluginResult {
	p := account.Provisioner{Store: store, Runner: r, Marketplace: marketplace}
	var out []PluginResult
	for _, acct := range accts {
		if !acct.Spec.Caps.Plugins {
			continue
		}
		res := PluginResult{Account: acct}
		installed, err := p.InstallPlugin(ctx, acct.ConfigDir())
		switch {
		case err != nil:
			res.Action, res.Err = PluginFailed, err
		case installed:
			res.Action = PluginInstalled
		default:
			res.Action = PluginUpdated
			if err := p.UpdatePlugin(ctx, acct.ConfigDir()); err != nil {
				res.Action, res.Err = PluginFailed, err
			}
		}
		out = append(out, res)
	}
	return out
}

// RemovePlugins uninstalls the squadrn plugin and its marketplace from every account whose
// provider supports plugins. Errors are per account; a missing plugin is not an error.
func RemovePlugins(ctx context.Context, accts []account.Account, r provider.Runner) map[string]error {
	errs := map[string]error{}
	for _, acct := range accts {
		if !acct.Spec.Caps.Plugins {
			continue
		}
		var err error
		if _, e := r.Output(ctx, acct.ConfigDir(), "plugin", "uninstall", account.PluginID); e != nil {
			err = e
		}
		_, _ = r.Output(ctx, acct.ConfigDir(), "plugin", "marketplace", "remove", account.MarketplaceName)
		errs[acct.Label()] = err
	}
	return errs
}
