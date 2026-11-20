package notify

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const sep = " · "

// Title is "<agent> · <account> · <repo>/<worktree>" (the grid pane instead of the worktree
// when there is one), leaving out what is unknown.
func Title(e Event) string {
	var parts []string
	for _, p := range []string{e.Agent, e.Account, e.Place()} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "Squadrn"
	}
	return strings.Join(parts, sep)
}

// Place is repo/worktree, repo/pane or repo.
func (e Event) Place() string {
	sub := e.Pane
	if sub == "" {
		sub = e.Worktree
	}
	switch {
	case e.Repo == "":
		return sub
	case sub == "" || sub == e.Repo:
		return e.Repo
	}
	return e.Repo + "/" + sub
}

// Body says what the user should do; cmd is the user's command name. Agent text appears only
// when preview is on.
func Body(e Event, cmd string, preview bool, now time.Time) string {
	var b string
	switch e.Kind {
	case Input:
		switch {
		case e.Reason == ReasonIdle:
			b = "Waiting for your reply"
		case strings.HasPrefix(e.Reason, "elicitation"):
			b = "Needs your input"
		case e.Tool != "":
			b = "Needs approval: " + e.Tool
		default:
			b = "Needs your approval"
		}
	case Failed:
		b = failure(e, cmd, now)
	case Done:
		b = "Done"
	case Info:
		b = clip(e.Message, 160)
		if b == "" {
			b = strings.ReplaceAll(e.Reason, "_", " ")
		}
		return b
	}
	if preview && e.Message != "" && e.Kind != Failed {
		b += sep + clip(e.Message, 160)
	}
	return b
}

func failure(e Event, cmd string, now time.Time) string {
	switch e.Reason {
	case ReasonRateLimit:
		b := "Usage limit"
		if !e.Resets.IsZero() {
			b += sep + "resets " + Clock(e.Resets, now)
		}
		return b + sep + cmd + " swap"
	case ReasonLogin, "oauth_org_not_allowed", "account_on_hold":
		ref := e.AccountRef
		if ref == "" {
			ref = e.Account
		}
		lead := "Login expired"
		switch e.Reason {
		case "oauth_org_not_allowed":
			lead = "Organization not allowed"
		case "account_on_hold":
			lead = "Account on hold"
		}
		return strings.TrimSpace(lead + sep + cmd + " account login " + ref)
	case ReasonBilling:
		return "Billing problem" + sep + "check the account's plan"
	case "", "unknown":
		return "Stopped on an error"
	}
	return "Stopped: " + strings.ReplaceAll(e.Reason, "_", " ")
}

// Clock shows t as 15:45 when it is today, else with the weekday (Thu 15:45).
func Clock(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Mon 15:04")
}

// clip shortens s to n runes on one line.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

var permissionTool = regexp.MustCompile(`(?i)permission to use ([\w.:-]+)`)

// ToolFromMessage finds the tool in an approval message ("Claude needs your permission to use
// Bash"); "" when it is not there.
func ToolFromMessage(msg string) string {
	if m := permissionTool.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

// Locate fills Repo and Worktree from cwd, without running git: a worktree under one of
// worktreeDirs (relative to the repository root, e.g. .claude/worktrees) names both; otherwise
// the nearest directory holding .git is the repository.
func (e *Event) Locate(worktreeDirs []string) {
	if e.Cwd == "" {
		return
	}
	slash := filepath.ToSlash(filepath.Clean(e.Cwd))
	for _, d := range worktreeDirs {
		marker := "/" + strings.Trim(d, "/") + "/"
		if i := strings.LastIndex(slash, marker); i > 0 {
			name, _, _ := strings.Cut(slash[i+len(marker):], "/")
			if name != "" {
				e.Repo, e.Worktree = pathBase(slash[:i]), name
				return
			}
		}
	}
	for dir := filepath.Clean(e.Cwd); ; {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			e.Repo = filepath.Base(dir)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	e.Repo = filepath.Base(filepath.Clean(e.Cwd))
}

func pathBase(slash string) string {
	if i := strings.LastIndex(slash, "/"); i >= 0 {
		return slash[i+1:]
	}
	return slash
}
