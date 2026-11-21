package cli

import (
	"slices"
	"testing"
)

func TestSplitCompletion(t *testing.T) {
	tests := []struct {
		name  string
		words []string
		want  completionLine
	}{
		{"nothing", nil, completionLine{}},
		{"first word", []string{""}, completionLine{rest: []string{}}},
		{"after the account", []string{"personal", "w"}, completionLine{account: "personal", rest: []string{}, toComplete: "w"}},
		{"subcommand wins over account", []string{"doctor", ""}, completionLine{rest: []string{"doctor"}}},
		{"account then subcommand", []string{"personal", "wt", ""}, completionLine{account: "personal", rest: []string{"wt"}}},
		{"-a account", []string{"-a", "x", "wt", ""}, completionLine{account: "x", rest: []string{"wt"}}},
		{"globals around the account", []string{"--no-color", "-C", "/r", "personal", "--dir=/s", ""}, completionLine{account: "personal", dir: "/s", rest: []string{}}},
		{"value of -a", []string{"-a", "p"}, completionLine{toComplete: "p", flagValue: true}},
		{"value of -C after the account", []string{"personal", "-C", ""}, completionLine{toComplete: "", flagValue: true}},
		{"agent subcommand", []string{"personal", "mcp", ""}, completionLine{account: "personal", rest: []string{"mcp"}}},
		{"prompt", []string{"--", "fix", ""}, completionLine{rest: []string{"--", "fix"}}},
		{"account twice", []string{"-a", "x", "personal", "-a", "y", ""}, completionLine{account: "x", rest: []string{"personal", "-a", "y"}}},
		{"account given twice after it", []string{"personal", "-a", "y", ""}, completionLine{account: "personal", invalid: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitCompletion(tt.words, table(false))
			if got.account != tt.want.account || got.dir != tt.want.dir || got.toComplete != tt.want.toComplete ||
				got.flagValue != tt.want.flagValue || got.invalid != tt.want.invalid || !slices.Equal(got.rest, tt.want.rest) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
