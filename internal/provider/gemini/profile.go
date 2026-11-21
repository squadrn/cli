package gemini

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/squadrn/squadrn/internal/provider"
)

// Profiles are settings.json keys and policy rules checked against Gemini CLI 0.70.0's
// configuration and policy engine references (ADR 0013 §7). Policy rules restrict Gemini's file
// tools, not what a shell command reads: `cat .env` through run_shell_command is not covered.
type Profiles struct{}

// PolicyFile is the profile's policy file in the account's state dir, passed to every session
// with --policy. It is not in policies/, which Gemini would load for main too.
const PolicyFile = "squadrn-policy.toml"

// fileTools are Gemini's tools that read files by path or pattern.
var fileTools = []string{"read_file", "read_many_files", "grep_search", "glob"}

var minimalReads = []string{"./.env", "./.env.*", "~/.ssh/**"}

// secretReads are the standard profile's denied reads, as in the Claude profiles.
var secretReads = []string{
	"./.env", "./.env.*", "./secrets/**", "~/.ssh/**", "~/.gnupg/**", "~/.aws/**", "~/.azure/**",
	"~/.kube/**", "~/.docker/config.json", "~/.npmrc", "~/.pypirc", "~/.git-credentials",
	"~/.config/gh/**", "~/.password-store/**",
}

// keptEnv are variables Gemini must never redact when its environment redaction is on: the
// git-level guard's GIT_CONFIG_* entries and the tmux pane notifications use.
func keptEnv() []any {
	out := []any{"GIT_CONFIG_COUNT", "TMUX", "TMUX_PANE"}
	for i := range 8 {
		out = append(out, fmt.Sprintf("GIT_CONFIG_KEY_%d", i), fmt.Sprintf("GIT_CONFIG_VALUE_%d", i))
	}
	return out
}

func (Profiles) Profile(name string, options []string, ctx provider.ProfileContext) (map[string]any, error) {
	general := map[string]any{}
	security := map[string]any{"allowedEnvironmentVariables": keptEnv()}
	doc := map[string]any{
		"security": security,
		// srn wt starts `gemini --worktree <name>`, which needs this.
		"experimental": map[string]any{"worktrees": true},
	}
	switch name {
	case provider.ProfileMinimal:
	case provider.ProfileStandard, provider.ProfileStrict:
		general["sessionRetention"] = map[string]any{"enabled": true, "maxAge": "30d"}
		security["disableYoloMode"] = true
		// Parallel panes would each run `npm install -g` at start; main keeps the user's choice.
		general["enableAutoUpdate"] = false
		if name == provider.ProfileStrict {
			general["sessionRetention"] = map[string]any{"enabled": true, "maxAge": "14d"}
			general["defaultApprovalMode"] = "default"
		}
	default:
		return nil, fmt.Errorf("unknown profile %q", name)
	}
	if ctx.Notify {
		// Squadrn notifies instead; Gemini's own notifications would come twice.
		general["enableNotifications"] = false
	}
	for _, o := range options {
		switch o {
		case "privacy":
			doc["privacy"] = map[string]any{"usageStatisticsEnabled": false}
			doc["telemetry"] = map[string]any{"enabled": false}
		case "no-attribution":
			// Gemini adds no attribution of its own.
		default:
			return nil, fmt.Errorf("unknown option %q", o)
		}
	}
	if len(general) > 0 {
		doc["general"] = general
	}
	return doc, nil
}

// rule is one [[rule]] of a policy file.
type rule struct {
	ToolName      any    `toml:"toolName"`
	CommandPrefix string `toml:"commandPrefix,omitempty"`
	ArgsPattern   string `toml:"argsPattern,omitempty"`
	Decision      string `toml:"decision"`
	Priority      int    `toml:"priority"`
	DenyMessage   string `toml:"denyMessage,omitempty"`
}

// ProfileFiles is the policy file: file tools may not read secrets or other accounts'
// credentials; the standard and strict profiles also ask before every git push.
func (Profiles) ProfileFiles(name string, _ []string, ctx provider.ProfileContext) (map[string][]byte, error) {
	reads := minimalReads
	switch name {
	case provider.ProfileMinimal:
	case provider.ProfileStandard, provider.ProfileStrict:
		reads = secretReads
	default:
		return nil, fmt.Errorf("unknown profile %q", name)
	}
	var rules []rule
	for _, p := range append(append([]string(nil), reads...), ctx.OtherCredentials...) {
		rules = append(rules, rule{
			ToolName: fileTools, ArgsPattern: pathPattern(p), Decision: "deny", Priority: 900,
			DenyMessage: "squadrn profile: reading " + p + " is denied",
		})
	}
	if name != provider.ProfileMinimal {
		rules = append(rules, rule{ToolName: shellTool, CommandPrefix: "git push", Decision: "ask_user", Priority: 100})
	}
	var buf bytes.Buffer
	buf.WriteString("# Written by squadrn for this account's profile; changes are overwritten.\n")
	if err := toml.NewEncoder(&buf).Encode(map[string]any{"rule": rules}); err != nil {
		return nil, err
	}
	return map[string][]byte{PolicyFile: buf.Bytes()}, nil
}

// pathPattern is an RE2 pattern finding a path in a tool's arguments as Gemini serialises them
// (JSON strings): "./x" anywhere below a directory, "~/x" in the home dir written as ~ or
// absolute; "/**" also matches everything below. Separators may be / or an escaped \.
func pathPattern(p string) string {
	dir := strings.HasSuffix(p, "/**")
	p = strings.TrimSuffix(p, "/**")
	p = strings.TrimPrefix(strings.TrimPrefix(p, "~/"), "./")
	var parts []string
	for _, seg := range strings.Split(p, "/") {
		q := regexp.QuoteMeta(seg)
		parts = append(parts, strings.ReplaceAll(q, `\*`, `[^/\\"]*`))
	}
	sep := `(?:/|\\\\)`
	body := `(?:^|[/~"]|\\\\)` + strings.Join(parts, sep)
	if dir {
		return body + `(?:` + sep + `|")`
	}
	return body + `"`
}
