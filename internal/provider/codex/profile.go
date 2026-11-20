package codex

import (
	"fmt"

	"github.com/squadrn/squadrn/internal/provider"
)

// Profiles are config.toml keys checked against Codex CLI 0.171.0's config schema
// (codex-rs/core/config.schema.json; ADR 0011). Codex has no per-path read rules, so the secret
// file denials of the Claude profiles have no equivalent: the sandbox and the environment policy
// are what limit an agent here.
type Profiles struct{}

func (Profiles) Profile(name string, options []string, _ provider.ProfileContext) (map[string]any, error) {
	env := map[string]any{"ignore_default_excludes": false}
	doc := map[string]any{storeKey: storeFile, "shell_environment_policy": env}
	switch name {
	case provider.ProfileMinimal:
	case provider.ProfileStandard, provider.ProfileStrict:
		env["inherit"] = "core"
		doc["approval_policy"] = "on-request"
		doc["sandbox_mode"] = "workspace-write"
		doc["sandbox_workspace_write"] = map[string]any{"network_access": false}
		if name == provider.ProfileStrict {
			doc["sandbox_mode"] = "read-only"
		}
	default:
		return nil, fmt.Errorf("unknown profile %q", name)
	}
	for _, o := range options {
		switch o {
		case "privacy":
			doc["analytics"] = map[string]any{"enabled": false}
			doc["feedback"] = map[string]any{"enabled": false}
		case "no-attribution":
			// Codex adds no attribution of its own that config.toml could turn off.
		default:
			return nil, fmt.Errorf("unknown option %q", o)
		}
	}
	return doc, nil
}
