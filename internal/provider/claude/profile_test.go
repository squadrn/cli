package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/squadrn/squadrn/internal/provider"
)

func denyOf(t *testing.T, doc map[string]any, key string) []string {
	t.Helper()
	perms, _ := doc["permissions"].(map[string]any)
	var out []string
	for _, v := range perms[key].([]any) {
		out = append(out, v.(string))
	}
	return out
}

func TestProfiles(t *testing.T) {
	ctx := provider.ProfileContext{
		OtherCredentials: []string{"~/.claude-other/.credentials.json"},
		OwnDirs:          []string{"~/.local/share/squadrn"},
		Hook:             "/opt/squadrn",
	}
	min, err := Profiles{}.Profile(provider.ProfileMinimal, nil, ctx)
	if err != nil || !slices.Equal(denyOf(t, min, "deny"), minimalDeny) || min["cleanupPeriodDays"] != nil {
		t.Fatalf("minimal = %v, %v", min, err)
	}
	std, err := Profiles{}.Profile(provider.ProfileStandard, []string{"privacy", "no-attribution"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	deny := denyOf(t, std, "deny")
	for _, want := range []string{"Read(~/.aws/**)", "Read(~/.claude-other/.credentials.json)", "Edit(~/.zshrc)", "Edit(~/.local/share/squadrn/**)"} {
		if !slices.Contains(deny, want) {
			t.Errorf("standard deny lacks %s", want)
		}
	}
	for _, r := range deny {
		if strings.HasPrefix(r, "Write") {
			t.Errorf("Write rules never match: %s", r)
		}
	}
	if !slices.Equal(denyOf(t, std, "ask"), standardAsk) || std["cleanupPeriodDays"] != 30 || std["enableAllProjectMcpServers"] != false {
		t.Errorf("standard = %v", std)
	}
	if std["env"].(map[string]any)["DISABLE_TELEMETRY"] != "1" || std["attribution"].(map[string]any)["pr"] != "" {
		t.Errorf("options = %v", std)
	}
	if _, has := std["model"]; has {
		t.Error("a profile never sets the model")
	}
	strict, err := Profiles{}.Profile(provider.ProfileStrict, nil, ctx)
	if err != nil {
		t.Fatal(err)
	}
	perms := strict["permissions"].(map[string]any)
	if perms["blockReadsOutsideWorkingDirectories"] != true || perms["disableBypassPermissionsMode"] != "disable" || strict["cleanupPeriodDays"] != 14 {
		t.Errorf("strict = %v", strict)
	}
	if _, has := strict["sandbox"]; has != sandboxAvailable {
		t.Errorf("sandbox = %v", strict["sandbox"])
	}
	b, _ := json.Marshal(strict["hooks"])
	if !strings.Contains(string(b), "hook config-change") {
		t.Errorf("hooks = %s", b)
	}
	if _, has := std["preferredNotifChannel"]; has {
		t.Error("Claude Code's own notifications are turned off only while squadrn's are on")
	}
	ctx.Notify = true
	for _, p := range []string{provider.ProfileMinimal, provider.ProfileStandard, provider.ProfileStrict} {
		doc, err := Profiles{}.Profile(p, nil, ctx)
		if err != nil || doc["preferredNotifChannel"] != "notifications_disabled" {
			t.Errorf("%s with notifications: %v %v", p, doc["preferredNotifChannel"], err)
		}
	}
	if _, err := (Profiles{}).Profile("lax", nil, ctx); err == nil {
		t.Error("unknown profile accepted")
	}
	if _, err := (Profiles{}).Profile(provider.ProfileMinimal, []string{"x"}, ctx); err == nil {
		t.Error("unknown option accepted")
	}
}

func TestSeedCopiesOnlyOnboardingKeys(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	if err := (Profiles{}).Seed(home, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude.json")); err == nil {
		t.Fatal("seeded from nothing")
	}
	main := `{"hasCompletedOnboarding":true,"lastOnboardingVersion":"2.1.0","oauthAccount":{"emailAddress":"x"},"mcpServers":{}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"lastOnboardingVersion":"own"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (Profiles{}).Seed(home, dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".claude.json"))
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["hasCompletedOnboarding"] != true || got["lastOnboardingVersion"] != "own" || got["oauthAccount"] != nil || got["mcpServers"] != nil {
		t.Errorf("seeded = %v", got)
	}
}
