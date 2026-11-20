package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/squadrn/squadrn/internal/account"
	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/inuse"
	"github.com/squadrn/squadrn/internal/usage"
)

// accountPurge logs acct out, deletes its dir for good and removes every record squadrn keeps of
// it. Records are only removed once the dir is gone, so a failed purge can simply be run again.
func (a *app) accountPurge(acct account.Account, yes bool) error {
	if err := a.store.CheckPurge(acct); err != nil {
		return err
	}
	if err := a.confirmPurge(acct, yes); err != nil {
		return err
	}
	label, retry := acct.Label(), fmt.Sprintf("%s account rm %s --purge", a.cmd, acct.Label())
	// Collected first: the usage record and the handoffs name the directories to clean.
	dirs := a.recordedDirs([]account.Account{acct}, func(h usage.Handoff) bool { return h.From == label || h.To == label })

	if err := a.logoutQuiet(acct); err != nil {
		a.warnf("cannot log %s out: %v; a stored login (e.g. a keychain item) may be left", label, err)
	} else {
		a.out("logged out %s\n", label)
	}
	if err := a.store.Purge(acct); err != nil {
		if pe := (*account.PurgeError)(nil); errors.As(err, &pe) {
			return fmt.Errorf("%w\nleft: %s with %d item(s) and every record of %s; close the programs using them and run: %s",
				err, a.tilde(acct.Dir), len(pe.Failed), label, retry)
		}
		return fmt.Errorf("%w\nleft: %s and every record of %s; nothing was deleted", err, a.tilde(acct.Dir), label)
	}
	a.out("deleted %s\n", a.tilde(acct.Dir))

	var removed, failed []string
	note := func(n int, one, many string, err error) {
		switch {
		case err != nil:
			failed = append(failed, fmt.Sprintf("%s: %v", many, err))
		case n == 1:
			removed = append(removed, "1 "+one)
		case n > 1:
			removed = append(removed, fmt.Sprintf("%d %s", n, many))
		}
	}
	if ok, err := usageStore(a.store.Home).Remove(acct.Provider, acct.Name); err != nil || ok {
		if err != nil {
			failed = append(failed, fmt.Sprintf("usage record (%s): %v", a.tilde(usageStore(a.store.Home).Dir), err))
		} else {
			removed = append(removed, "usage record")
		}
	}
	n, err := a.ledger().Remove(label)
	note(n, "handoff entry", "handoff entries", err)
	n, err = usage.DropLines(filepath.Join(config.DirsFor(a.store.Home, config.Slug).State, configChangeLog), func(line []byte) bool {
		var e struct {
			ConfigDir string `json:"config_dir"`
		}
		return json.Unmarshal(line, &e) == nil && e.ConfigDir != "" && fsx.SamePath(e.ConfigDir, acct.Dir)
	})
	note(n, "config change entry", "config change entries", err)
	inUse := 0
	for _, d := range dirs {
		n, live := inuse.Forget(d, label)
		inUse += n
		for _, e := range live {
			a.warnf("kept the in-use record of %s in %s: it is still running", a.sessionDesc(e), a.tilde(d))
		}
	}
	note(inUse, "in-use record", "in-use records", nil)
	if a.cache.Forget(label) {
		removed = append(removed, "cached login status")
	}
	if len(removed) > 0 {
		a.out("removed %s\n", strings.Join(removed, ", "))
	}
	if len(failed) > 0 {
		for _, f := range failed {
			a.warnf("cannot remove the %s", f)
		}
		return fmt.Errorf("%s is deleted but some of its records are left (above); remove them by hand", label)
	}
	return nil
}

// confirmPurge asks for the account's label at a terminal, even with --yes: a purge cannot be
// undone. Without a terminal --yes is required.
func (a *app) confirmPurge(acct account.Account, yes bool) error {
	in, out, done, ok := a.terminalIO()
	if !ok {
		if !yes {
			return usagef("permanently deleting %s and every record of %s needs a terminal or --purge --yes", a.tilde(acct.Dir), acct.Label())
		}
		return nil
	}
	defer done()
	_, _ = fmt.Fprintf(out, "Permanently delete %s and every record of %s (no trash, cannot be undone).\nType %s to confirm: ",
		a.tilde(acct.Dir), acct.Label(), acct.Label())
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.TrimSpace(line) != acct.Label() {
		return errAborted
	}
	return nil
}
