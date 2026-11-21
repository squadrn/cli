package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/provider"
)

// Profiles are settings.json keys checked against the Claude Code docs (settings, permissions,
// sandboxing, hooks, env vars; ADR 0011). Read rules also cover the Bash file commands Claude
// Code recognises; Edit rules cover every file-editing tool (Write rules never match).
type Profiles struct{}

var minimalDeny = []string{"Read(./.env)", "Read(./.env.*)", "Read(~/.ssh/**)"}

var secretReads = []string{
	"./.env", "./.env.*", "./secrets/**", "~/.ssh/**", "~/.gnupg/**", "~/.aws/**", "~/.azure/**",
	"~/.kube/**", "~/.docker/config.json", "~/.npmrc", "~/.pypirc", "~/.git-credentials",
	"~/.config/gh/**", "~/.password-store/**",
}

var protectedEdits = []string{
	"~/.ssh/**", "~/.bashrc", "~/.bash_profile", "~/.zshrc", "~/.zprofile", "~/.profile",
	"~/.config/fish/config.fish",
}

var standardAsk = []string{"Bash(git push *)", "Bash(git reset --hard *)", "Bash(rm -rf *)"}

func (Profiles) Profile(name string, options []string, ctx provider.ProfileContext) (map[string]any, error) {
	doc := map[string]any{}
	perms := map[string]any{}
	switch name {
	case provider.ProfileMinimal:
		perms["deny"] = strs(minimalDeny)
	case provider.ProfileStandard, provider.ProfileStrict:
		var deny []string
		for _, p := range append(slices.Clone(secretReads), ctx.OtherCredentials...) {
			deny = append(deny, "Read("+p+")")
		}
		for _, p := range protectedEdits {
			deny = append(deny, "Edit("+p+")")
		}
		for _, d := range ctx.OwnDirs {
			deny = append(deny, "Edit("+d+"/**)")
		}
		perms["deny"] = strs(deny)
		perms["ask"] = strs(standardAsk)
		doc["enableAllProjectMcpServers"] = false
		doc["cleanupPeriodDays"] = 30
		if name == provider.ProfileStrict {
			perms["blockReadsOutsideWorkingDirectories"] = true
			perms["defaultMode"] = "default"
			perms["disableBypassPermissionsMode"] = "disable"
			doc["cleanupPeriodDays"] = 14
			if sandboxAvailable {
				doc["sandbox"] = map[string]any{"enabled": true, "allowUnsandboxedCommands": false, "failIfUnavailable": true}
			}
			if bin, err := statusLineWord(ctx.Hook, ctx.Home); ctx.Hook != "" && err == nil {
				hook := map[string]any{"type": "command", "command": bin + " hook config-change"}
				doc["hooks"] = map[string]any{"ConfigChange": []any{map[string]any{"hooks": []any{hook}}}}
			}
		}
	default:
		return nil, fmt.Errorf("unknown profile %q", name)
	}
	doc["permissions"] = perms
	if ctx.Notify {
		// Squadrn notifies instead; without this, iTerm2, Ghostty and Kitty would show both.
		doc["preferredNotifChannel"] = "notifications_disabled"
	}
	for _, o := range options {
		switch o {
		case "privacy":
			doc["env"] = map[string]any{"DISABLE_TELEMETRY": "1", "DISABLE_ERROR_REPORTING": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}
		case "no-attribution":
			doc["attribution"] = map[string]any{"commit": "", "pr": ""}
		default:
			return nil, fmt.Errorf("unknown option %q", o)
		}
	}
	return doc, nil
}

func strs(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// onboarding is the allowlist of first-run state copied from main's ~/.claude.json into a new
// account's <dir>/.claude.json (internal keys; Claude Code would otherwise onboard again).
var onboarding = []string{"hasCompletedOnboarding", "lastOnboardingVersion"}

// Seed copies the onboarding keys main has into dir/.claude.json, keeping what is there.
func (Profiles) Seed(home, dir string) error {
	src, err := readJSONObject(filepath.Join(home, provider.Claude.MCPConfig))
	if err != nil || len(src) == 0 {
		return err
	}
	dstPath := filepath.Join(dir, provider.Claude.MCPConfig)
	dst, err := readJSONObject(dstPath)
	if err != nil {
		return err
	}
	changed := false
	for _, k := range onboarding {
		if v, ok := src[k]; ok {
			if _, has := dst[k]; !has {
				dst[k] = v
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	b, err := json.MarshalIndent(dst, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteBytes(dstPath, append(b, '\n'), 0o600)
}

func readJSONObject(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	} else if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	return m, nil
}
