package cli

import "testing"

func TestTitleState(t *testing.T) {
	for _, tt := range []struct{ account, title, want string }{
		{"gemini/work", "✋ Action Required (api)", "input"},
		{"gemini/work", "⏲  Working… (api)", "working"},
		{"gemini/work", " ◇  Ready (api)", "ready"},
		{"gemini/work", "zsh", ""},
		{"gemini/work", "", ""},
		{"claude/work", "✋ Action Required", ""},
		{"nope/x", "✋", ""},
	} {
		if got := titleState(tt.account, tt.title); got != tt.want {
			t.Errorf("titleState(%q, %q) = %q, want %q", tt.account, tt.title, got, tt.want)
		}
	}
}
