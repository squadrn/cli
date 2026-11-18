// Package install places releases on disk: command names, migration from previous product
// names, and the plugin in every account. It returns what it did; cli reports it.
package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"sprawling-agents/internal/account"
	"sprawling-agents/internal/config"
	"sprawling-agents/internal/fsx"
	"sprawling-agents/internal/provider"
	"sprawling-agents/internal/release"
)

var (
	ErrNameTaken     = errors.New("command name is taken")
	ErrDuplicateName = errors.New("command name given twice")
)

// LoadConfig returns the config as written. Without one (fresh is true) it adopts the settings
// of a previous product's config, but not its command names: those belonged to the old product.
func LoadConfig(path string, legacy []string) (cfg config.Config, migratedFrom string, fresh bool, err error) {
	if _, err := os.Stat(path); err == nil {
		c, err := config.Read(path)
		return c, "", false, err
	}
	for _, lp := range legacy {
		if _, err := os.Stat(lp); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return config.Config{}, "", false, err
		}
		old, err := config.Read(lp)
		if err != nil {
			return config.Config{}, "", false, err
		}
		c := config.Config{SharedMCP: old.SharedMCP, ProtectedBranches: old.ProtectedBranches, DefaultProvider: old.DefaultProvider, GitHooks: old.GitHooks}
		return c, lp, true, nil
	}
	return config.Config{}, "", true, nil
}

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
// (or the name was accepted before, or it is sprawl itself).
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

// Moved is one directory of a previous product moved into the backup dir.
type Moved struct {
	Name   string // previous product name
	Backup string // where its files went
}

// MigrateLegacy moves the install, config and state dirs of previous product names into a
// backup under the state dir (their guard logs into legacy-logs/) and drops their caches.
func MigrateLegacy(home string, now time.Time) ([]Moved, []error) {
	var moved []Moved
	var errs []error
	state := config.DirsFor(home, config.Slug).State
	for _, name := range config.PreviousNames {
		old := config.DirsFor(home, name)
		if log := filepath.Join(old.State, "guard.log"); fsx.IsFile(log) {
			if err := os.MkdirAll(filepath.Join(state, "legacy-logs"), 0o700); err == nil {
				_ = os.Rename(log, filepath.Join(state, "legacy-logs", name+"-guard.log"))
			}
		}
		_ = os.RemoveAll(old.Cache)
		backup := filepath.Join(state, "legacy-"+name+"-"+now.Format("20060102-150405"))
		any := false
		for _, dir := range []string{old.Data, old.State, old.Config} {
			if !fsx.IsDir(dir) {
				continue
			}
			if err := os.MkdirAll(backup, 0o700); err != nil {
				errs = append(errs, err)
				break
			}
			dst := filepath.Join(backup, filepath.Base(filepath.Dir(dir))+"-"+filepath.Base(dir))
			if err := os.Rename(dir, dst); err != nil {
				errs = append(errs, fmt.Errorf("move %s: %w", dir, err))
				continue
			}
			any = true
		}
		if any {
			moved = append(moved, Moved{Name: name, Backup: backup})
		}
	}
	return moved, errs
}

// PluginAction is what happened to the sprawl plugin in one account.
type PluginAction string

const (
	PluginInstalled PluginAction = "installed"
	PluginUpdated   PluginAction = "updated"
	PluginFailed    PluginAction = "failed"
)

type PluginResult struct {
	Account account.Account
	Action  PluginAction
	Removed []string // plugins of previous product names that were uninstalled
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
		removed, err := p.RemoveLegacyPlugins(ctx, acct.ConfigDir())
		res.Removed = removed
		if err != nil {
			res.Action, res.Err = PluginFailed, fmt.Errorf("remove old plugins: %w", err)
			out = append(out, res)
			continue
		}
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

// RemovePlugins uninstalls the sprawl plugin and its marketplace from every account whose
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
