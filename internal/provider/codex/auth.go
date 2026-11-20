package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/squadrn/squadrn/internal/provider"
)

// accountReadTimeout bounds the app-server call that adds the email and plan to a login.
const accountReadTimeout = 5 * time.Second

// Auth reads `codex login status`, which exits 0 when logged in and prints the method
// ("Logged in using ChatGPT", "Not logged in"), then asks the app server (account/read) for the
// email and plan, which the status line does not show. The second step is best effort.
func (c CLI) Auth(ctx context.Context, configDir string) (provider.AuthStatus, error) {
	out, err := c.Output(ctx, configDir, "login", "status")
	// Codex prints the status on stderr, which a failure's error carries.
	text := string(out)
	if err != nil {
		text += "\n" + err.Error()
	}
	s, perr := parseLoginStatus(text, err == nil)
	if perr != nil {
		if err != nil {
			return provider.AuthStatus{}, err
		}
		return provider.AuthStatus{}, perr
	}
	if !s.LoggedIn {
		return s, nil
	}
	actx, cancel := context.WithTimeout(ctx, accountReadTimeout)
	defer cancel()
	if r, err := c.appServer(actx, configDir, request{Method: "account/read", Params: map[string]bool{"refreshToken": false}}); err == nil && r[0].Err == nil {
		s = withAccount(s, r[0].Result)
	}
	return s, nil
}

// parseLoginStatus reads the output of `codex login status`; ok is a zero exit. Output it does
// not recognise is decided by the exit status alone.
func parseLoginStatus(out string, ok bool) (provider.AuthStatus, error) {
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasSuffix(line, "Not logged in"):
			return provider.AuthStatus{}, nil
		case strings.HasPrefix(line, "Logged in using "):
			method := strings.TrimPrefix(line, "Logged in using ")
			// "an API key - sk-…": never keep the key, even masked.
			method, _, _ = strings.Cut(method, " - ")
			method = strings.TrimPrefix(strings.TrimPrefix(method, "an "), "a ")
			return provider.AuthStatus{LoggedIn: true, AuthMethod: method}, nil
		}
	}
	if ok {
		return provider.AuthStatus{LoggedIn: true}, nil
	}
	if strings.TrimSpace(out) == "" {
		return provider.AuthStatus{}, errors.New("codex login status printed nothing")
	}
	return provider.AuthStatus{}, nil
}

// withAccount adds the app server's account/read result to s.
func withAccount(s provider.AuthStatus, raw json.RawMessage) provider.AuthStatus {
	var r struct {
		Account *struct {
			Type          string `json:"type"`
			Email         string `json:"email"`
			PlanType      string `json:"planType"`
			WorkspaceName string `json:"workspaceName"`
		} `json:"account"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Account == nil {
		return s
	}
	a := r.Account
	switch a.Type {
	case "chatgpt":
		s.AuthMethod = "ChatGPT"
		s.Email, s.OrgName = a.Email, a.WorkspaceName
		if a.PlanType != "" && a.PlanType != "unknown" {
			s.SubscriptionType = a.PlanType
		}
	case "apiKey":
		s.AuthMethod = "API key"
	}
	return s
}
