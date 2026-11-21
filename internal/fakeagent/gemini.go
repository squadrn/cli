package fakeagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Gemini fakes Gemini CLI. The account is GEMINI_CLI_HOME, a home whose .gemini/ holds the
// state; unset, it is ~/.gemini (main). Gemini has no auth commands: the login is a session run
// with --skip-trust, which writes oauth_creds.json and google_accounts.json (and selects
// oauth-personal in a settings.json without comments that selects nothing) unless
// FAKE_LOGIN_FAILS. A session whose environment still carries
// GEMINI_FORCE_ENCRYPTED_FILE_STORAGE logs encrypted-storage=<value>.
func Gemini(args []string) int {
	home := os.Getenv("GEMINI_CLI_HOME")
	cfg := home
	if home == "" {
		cfg, home = "<unset>", os.Getenv("HOME")
	}
	state := filepath.Join(home, ".gemini")
	cmd := strings.Join(args, " ")
	extra := ""
	if v, ok := os.LookupEnv("GEMINI_FORCE_ENCRYPTED_FILE_STORAGE"); ok {
		extra = " encrypted-storage=" + v
	}
	switch cmd {
	case "--version":
		v := os.Getenv("FAKE_VERSION")
		if v == "" {
			v = "0.70.0"
		}
		fmt.Println(v)
	case "--skip-trust":
		loginWait()
		logCall("cfg=%s args=%s%s", cfg, cmd, extra)
		if os.Getenv("FAKE_LOGIN_FAILS") != "" {
			return 0
		}
		_ = os.MkdirAll(state, 0o700)
		_ = os.WriteFile(filepath.Join(state, "oauth_creds.json"), []byte(`{"access_token":"fake","refresh_token":"fake","token_type":"Bearer"}`), 0o600)
		_ = os.WriteFile(filepath.Join(state, "google_accounts.json"), []byte(`{"active":"me@`+filepath.Base(home)+`","old":[]}`), 0o600)
		selectOAuth(filepath.Join(state, "settings.json"))
	default:
		fmt.Printf("cfg=%s args=%s\n", cfg, cmd)
		logCall("cfg=%s pwd=%s args=%s%s", cfg, workdir(), cmd, extra)
		return session("")
	}
	return 0
}

// selectOAuth sets security.auth.selectedType = oauth-personal, as the CLI's auth dialog does,
// in a settings file that selects nothing yet and has no comments.
func selectOAuth(path string) {
	doc := map[string]any{}
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &doc) != nil {
		return
	}
	sec, _ := doc["security"].(map[string]any)
	if sec == nil {
		sec = map[string]any{}
	}
	auth, _ := sec["auth"].(map[string]any)
	if auth == nil {
		auth = map[string]any{}
	}
	if auth["selectedType"] != nil {
		return
	}
	auth["selectedType"] = "oauth-personal"
	sec["auth"] = auth
	doc["security"] = sec
	b, _ := json.MarshalIndent(doc, "", "  ")
	_ = os.WriteFile(path, append(b, '\n'), 0o600)
}
