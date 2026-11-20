package cli

import (
	"strings"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/provider"
)

// settingsFormat is the settings file format of spec's CLI; nil when accounts have no copy.
func settingsFormat(spec provider.Spec) provider.SettingsFormat {
	d, _ := provider.DriverOf(spec.ID)
	return d.Settings
}

// syncSettings brings acct's own settings file up to date with main's (ADR 0011). A status line
// that records usage, inherited from main when an rc.2 link becomes a copy, stays squadrn's.
func (a *app) syncSettings(acct account.Account) (account.SettingsReport, error) {
	f := settingsFormat(acct.Spec)
	rep, err := a.store.SyncSettings(acct, f, nil, a.now())
	if err != nil || rep.Backup == "" {
		return rep, err
	}
	if sl := statusLineOf(acct.Spec); sl != nil && a.usageTracking(acct) {
		err = account.ClaimSetting(acct, f, sl.Key())
	}
	return rep, err
}

// noteSettings tells the user, once, what a session-start sync did that they should know about.
func (a *app) noteSettings(acct account.Account, rep account.SettingsReport) {
	if rep.Backup != "" {
		a.errf("%s: %s now has its own settings file, kept in step with the main account's (the link was moved to %s)\n", a.cmd, acct.Label(), a.tilde(rep.Backup))
	}
	if len(rep.Kept) > 0 {
		a.errf("%s: %s: kept your change to %s; it no longer follows the main account\n", a.cmd, acct.Label(), strings.Join(rep.Kept, ", "))
	}
}

// printSettings lists what a sync changed, for account add.
func (a *app) printSettings(acct account.Account, rep account.SettingsReport) {
	path := a.tilde(account.SettingsPath(acct))
	switch {
	case rep.Backup != "":
		a.out("  settings %s is the account's own copy now (link moved to %s)\n", path, a.tilde(rep.Backup))
	case rep.Created:
		a.out("  settings %s from main\n", path)
	case len(rep.Written) > 0 || len(rep.Removed) > 0:
		a.out("  settings %s updated: %s\n", path, strings.Join(append(rep.Written, rep.Removed...), ", "))
	}
	if len(rep.Kept) > 0 {
		a.out("  settings kept your change to %s (it no longer follows main)\n", strings.Join(rep.Kept, ", "))
	}
}
