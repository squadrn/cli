package gemini

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/squadrn/squadrn-cli/internal/proc"
	"github.com/squadrn/squadrn-cli/internal/provider"
)

// minNode is the oldest Node.js major version Gemini CLI runs on.
const minNode = 20

// CheckCredentials: Gemini keeps Google tokens in the account's oauth_creds.json as long as the
// encrypted storage switch is off, which squadrn ensures for every run (env); its fallback file
// in the account means it was on once.
func (c CLI) CheckCredentials(configDir string) error {
	if _, err := os.Stat(filepath.Join(c.state(configDir), "gemini-credentials.json")); err == nil {
		return errors.New("gemini-credentials.json found: encrypted storage was on, and its keychain entry is shared by every account")
	}
	return nil
}

// Diagnose checks Node.js and the user's environment.
func (c CLI) Diagnose(ctx context.Context) []provider.Notice {
	return diagnose(ctx, os.Getenv, nodeVersion)
}

func diagnose(ctx context.Context, getenv func(string) string, node func(context.Context) (string, error)) []provider.Notice {
	var out []provider.Notice
	switch v, err := node(ctx); {
	case err != nil:
		out = append(out, provider.Notice{Warn: true, Text: "node not found: Gemini CLI needs Node.js " + strconv.Itoa(minNode) + " or newer", Fix: "install Node.js " + strconv.Itoa(minNode) + "+"})
	case nodeMajor(v) < minNode:
		out = append(out, provider.Notice{Warn: true, Text: "node " + v + " is too old: Gemini CLI needs Node.js " + strconv.Itoa(minNode) + " or newer", Fix: "install Node.js " + strconv.Itoa(minNode) + "+"})
	default:
		out = append(out, provider.Notice{Text: "node " + v + " (Gemini CLI)"})
	}
	if getenv(encryptedStorageEnv) != "" {
		out = append(out, provider.Notice{
			Warn: true,
			Text: encryptedStorageEnv + " is set: outside squadrn, Gemini keeps every account's login in one shared keychain entry (squadrn removes it for its sessions)",
			Fix:  "unset " + encryptedStorageEnv,
		})
	}
	if getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" {
		out = append(out, provider.Notice{
			Warn: true,
			Text: "GOOGLE_APPLICATION_CREDENTIALS is set: every Gemini account using Google Cloud credentials uses that one file",
			Fix:  "unset it, or accept that those accounts share it",
		})
	}
	return out
}

// AccountNotices: layer 2 of the push guard cannot run in Gemini sessions, and Login with Google
// works only for a Code Assist Standard or Enterprise license (ADR 0013).
func (c CLI) AccountNotices(configDir string) []provider.Notice {
	out := []provider.Notice{{
		Warn: true,
		Text: "no layer-2 push guard: Gemini CLI turns git hooks off (core.hooksPath=\"\") in the shell commands it runs; the pre-tool hook (layer 1) and the server's branch protection remain",
	}}
	if n, ok := consumerLogin(c.state(configDir), os.Getenv); ok {
		out = append(out, n)
	}
	return out
}

// consumerLogin warns about an account that signs in with Google without a Google Cloud project:
// since 2026-06-18 Google refuses Gemini CLI for personal accounts, Google AI Pro and Ultra
// ("This client is no longer supported for Gemini Code Assist for individuals"); only Code Assist
// Standard and Enterprise licenses, which name their project, still sign in.
func consumerLogin(state string, getenv func(string) string) (provider.Notice, bool) {
	if sel, err := selectedType(filepath.Join(state, provider.Gemini.Settings)); err != nil || sel != authOAuth {
		return provider.Notice{}, false
	}
	if getenv("GOOGLE_CLOUD_PROJECT") != "" || getenv("GOOGLE_CLOUD_PROJECT_ID") != "" {
		return provider.Notice{}, false
	}
	return provider.Notice{
		Warn: true,
		Text: "Login with Google without GOOGLE_CLOUD_PROJECT: since 2026-06-18 Google refuses personal accounts, Google AI Pro and Ultra in Gemini CLI; it serves Code Assist Standard and Enterprise licenses",
		Fix:  "set GOOGLE_CLOUD_PROJECT to the license's project, or use a paid Gemini API key or Vertex AI; personal accounts: Antigravity CLI (agy)",
	}, true
}

var nodeVersionPattern = regexp.MustCompile(`^v?(\d+)\.`)

// nodeMajor is the major version of `node --version` output (v22.1.0); 0 when unreadable.
func nodeMajor(v string) int {
	m := nodeVersionPattern.FindStringSubmatch(v)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func nodeVersion(ctx context.Context) (string, error) {
	bin, err := exec.LookPath("node")
	if err != nil {
		return "", err
	}
	out, err := proc.Output(ctx, proc.Options{}, bin, "--version")
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(out)), nil
}
