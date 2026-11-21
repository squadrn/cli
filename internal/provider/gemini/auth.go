package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/squadrn/squadrn/internal/provider"
)

// Auth types of security.auth.selectedType (Gemini CLI 0.70.0).
const (
	authOAuth  = "oauth-personal"
	authAPIKey = "gemini-api-key"
	authVertex = "vertex-ai"
)

// Auth reads the login from the account's files: Gemini has no auth status command. Logged in
// with Google when oauth_creds.json holds a refresh token and settings select oauth-personal;
// with an API key (billed per use) when settings select one and the environment provides it.
// Token values are never kept.
func (c CLI) Auth(_ context.Context, configDir string) (provider.AuthStatus, error) {
	return readAuth(c.state(configDir), os.Getenv)
}

func readAuth(state string, getenv func(string) string) (provider.AuthStatus, error) {
	selected, err := selectedType(filepath.Join(state, provider.Gemini.Settings))
	if err != nil {
		return provider.AuthStatus{}, err
	}
	switch selected {
	case authOAuth:
		ok, err := hasRefreshToken(filepath.Join(state, "oauth_creds.json"))
		if err != nil || !ok {
			return provider.AuthStatus{}, err
		}
		return provider.AuthStatus{LoggedIn: true, AuthMethod: "Google account", Email: activeAccount(filepath.Join(state, "google_accounts.json"))}, nil
	case authAPIKey:
		if getenv("GEMINI_API_KEY") == "" {
			return provider.AuthStatus{}, nil
		}
		return provider.AuthStatus{LoggedIn: true, AuthMethod: "Gemini API key"}, nil
	case authVertex:
		switch {
		case getenv("GOOGLE_API_KEY") != "":
			return provider.AuthStatus{LoggedIn: true, AuthMethod: "Vertex AI API key"}, nil
		case getenv("GOOGLE_CLOUD_PROJECT") != "" && getenv("GOOGLE_CLOUD_LOCATION") != "":
			return provider.AuthStatus{LoggedIn: true, AuthMethod: "Vertex AI API (project " + getenv("GOOGLE_CLOUD_PROJECT") + ")"}, nil
		}
		return provider.AuthStatus{}, nil
	case "":
		return provider.AuthStatus{}, nil
	}
	// cloud-shell, compute-default-credentials, gateway: the environment provides the login,
	// which squadrn cannot check; the CLI reports a missing one itself.
	return provider.AuthStatus{LoggedIn: true, AuthMethod: selected}, nil
}

// selectedType is security.auth.selectedType in a settings file; "" when unset or missing.
func selectedType(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	doc, err := Settings{}.Decode(b)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	sec, _ := doc["security"].(map[string]any)
	auth, _ := sec["auth"].(map[string]any)
	v, _ := auth["selectedType"].(string)
	return v, nil
}

// hasRefreshToken reports whether the token file holds a refresh token, without keeping it.
func hasRefreshToken(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var creds struct {
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal(b, &creds) != nil {
		// Never echo the file: a parse error can quote it.
		return false, fmt.Errorf("%s is not valid JSON", path)
	}
	return creds.RefreshToken != "", nil
}

// activeAccount is the signed-in email in google_accounts.json; "" when unknown.
func activeAccount(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var accts struct {
		Active string `json:"active"`
	}
	if json.Unmarshal(b, &accts) != nil {
		return ""
	}
	return accts.Active
}
