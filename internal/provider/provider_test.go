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
