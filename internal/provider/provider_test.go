package provider

import "testing"

func TestMetered(t *testing.T) {
	tests := []struct {
		s    AuthStatus
		want bool
	}{
		{AuthStatus{LoggedIn: true, AuthMethod: "api_key"}, true},
		{AuthStatus{LoggedIn: true, AuthMethod: "claude.ai", SubscriptionType: "max"}, false},
		{AuthStatus{LoggedIn: true, AuthMethod: "api_key", SubscriptionType: "pro"}, false},
		{AuthStatus{LoggedIn: true}, false},
		{AuthStatus{AuthMethod: "api_key"}, false},
	}
	for _, tt := range tests {
		if got := tt.s.Metered(); got != tt.want {
			t.Errorf("%+v: %v", tt.s, got)
		}
	}
}

func TestRegisterAttachesDrivers(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registering an unknown provider did not panic")
		}
	}()
	if _, ok := DriverOf("nope"); ok {
		t.Error("DriverOf(nope) found a driver")
	}
	Register("nope", Driver{})
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]string{
		"2.1.329 (Claude Code)\n": "2.1.329",
		"codex-cli 0.171.0":       "0.171.0",
		"cfg=<unset> args=x":      "",
		"v1.2.3-rc.1":             "1.2.3-rc.1",
	} {
		if got := ParseVersion([]byte(in)); got != want {
			t.Errorf("ParseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
