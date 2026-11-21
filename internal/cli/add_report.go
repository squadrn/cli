package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/squadrn/squadrn-cli/internal/account"
)

// addReporter is where account add reports, so its logic never knows where it prints: plain lines
// (the flag path), JSON (--json) or the wizard's frame.
type addReporter interface {
	// header names the CLI and the account before anything is asked or done.
	header(p *addPlan)
	// planLine is one line of the plan: what will be applied.
	planLine(label, text string)
	note(text string)
	warn(text string)
	// info is text from the agent CLI (its login hint).
	info(text string)
	loginStart()
	// failed reports a failed step and returns the command's error.
	failed(acct account.Account, step addStep, err error) error
	// done reports the result once everything ran.
	done(p *addPlan, res addResult) error
}

func (a *app) addReporter(asJSON bool) addReporter {
	if asJSON {
		return jsonReporter{a}
	}
	return lineReporter{a}
}

// lineReporter prints plain lines on stdout, the questions on stderr.
type lineReporter struct{ a *app }

func (r lineReporter) header(p *addPlan) {
	a := r.a
	head := p.acct.Spec.Name
	if p.version != "" {
		head += " " + p.version
	}
	switch {
	case p.main:
		a.out("%s · %s, the default account → %s\n", head, p.acct.Label(), a.tilde(p.acct.Dir))
	case p.isNew:
		a.out("%s · new account %s → %s\n", head, p.acct.Label(), a.tilde(p.acct.Dir))
	default:
		a.out("%s · %s → %s\n", head, p.acct.Label(), a.tilde(p.acct.Dir))
	}
}

func (r lineReporter) planLine(label, text string) {
	r.a.out("%s%-14s %s\n", r.a.padding(), label, text)
}

func (r lineReporter) note(text string) {
	r.a.out("%s %s\n", r.a.paint(r.a.colorOut, "32", "✓"), text)
}

func (r lineReporter) warn(text string) {
	r.a.out("%s %s\n", r.a.paint(r.a.colorOut, "33", "!"), text)
}

func (r lineReporter) info(text string) { r.a.out("%s\n", text) }

func (r lineReporter) loginStart() {
	r.a.out("%s opening login…\n", r.a.paint(r.a.colorOut, "36", "↗"))
}

// failed says which step failed, what state the account is in and how to retry.
func (r lineReporter) failed(acct account.Account, step addStep, err error) error {
	a := r.a
	a.out("%s %s failed: %v\n", a.paint(a.colorOut, "31", "✗"), step.name, err)
	if step.created {
		a.out("  state: %s exists; what is missing is repaired by a re-run\n", a.tilde(acct.Dir))
	} else {
		a.out("  state: nothing was created\n")
	}
	a.out("  retry: %s account add %s\n", a.cmd, acct.Label())
	return errQuiet
}

func (r lineReporter) done(p *addPlan, res addResult) error {
	a := r.a
	if (p.main || !p.isNew) && res.changes == 0 {
		a.out("%s %s is up to date\n", a.paint(a.colorOut, "32", "✓"), p.acct.Label())
	}
	next := "start: " + a.startCommand(p.acct)
	if !p.status.LoggedIn {
		next = "log in: " + a.cmd + " account login " + p.acct.Label()
	}
	a.out("done in %.1fs · %s · check: %s doctor %s\n", (time.Since(p.start) - p.waited).Seconds(), next, a.cmd, p.acct.Label())
	return nil
}

// jsonReporter prints nothing but the result: one JSON object on stdout. Warnings go to stderr.
type jsonReporter struct{ a *app }

func (jsonReporter) header(*addPlan)         {}
func (jsonReporter) planLine(string, string) {}
func (jsonReporter) note(string)             {}
func (r jsonReporter) warn(text string)      { r.a.warnf("%s", text) }
func (r jsonReporter) info(text string)      { r.a.errf("%s\n", text) }
func (jsonReporter) loginStart()             {}

func (r jsonReporter) failed(acct account.Account, step addStep, err error) error {
	return fixError{msg: fmt.Sprintf("%s failed: %v", step.name, err), how: "retry", fix: r.a.cmd + " account add " + acct.Label()}
}

func (r jsonReporter) done(p *addPlan, res addResult) error {
	return r.a.printJSONLine(r.a.result(p, res))
}

// fixError is an error with the command that fixes it; --json prints the two apart.
type fixError struct {
	msg, how, fix string
	err           error
}

func (e fixError) Error() string { return e.msg + "; " + e.how + ": " + e.fix }
func (e fixError) Unwrap() error { return e.err }

// reported is an error already printed (as JSON); it keeps the exit code of the error it wraps.
type reported struct{ error }

func (e reported) Unwrap() error { return e.error }

// jsonErrors prints a failed --json command's error as {"error": …, "fix": …} on stdout.
func (a *app) jsonErrors(asJSON bool, err error) error {
	if !asJSON || err == nil || silent(err) {
		return err
	}
	out := struct {
		Error string `json:"error"`
		Fix   string `json:"fix,omitempty"`
	}{Error: err.Error()}
	var fe fixError
	if errors.As(err, &fe) {
		out.Error, out.Fix = fe.msg, fe.fix
	}
	var nf account.NotFoundError
	if errors.As(err, &nf) {
		out.Fix = a.cmd + " account add " + nf.Ref.String()
	}
	if perr := a.printJSONLine(out); perr != nil {
		return perr
	}
	return reported{err}
}

// printJSONLine prints v as one line of JSON.
func (a *app) printJSONLine(v any) error {
	return json.NewEncoder(a.stdout).Encode(v)
}
