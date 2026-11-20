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

// syncSettings brings acct's own settings file up to date with main's and its profile (ADR
// 0011). A status line that records usage, inherited from main when an rc.2 link becomes a
// copy, stays squadrn's.
func (a *app) syncSettings(acct account.Account) (account.SettingsReport, error) {
	f := settingsFormat(acct.Spec)
	rep, err := a.syncSettingsAs(acct, acct)
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
