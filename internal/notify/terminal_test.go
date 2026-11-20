package notify

import "testing"

func TestTerminal(t *testing.T) {
	for _, tc := range []struct {
		env            map[string]string
		name, protocol string
	}{
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, "iTerm2", osc9},
		{map[string]string{"TERM_PROGRAM": "WezTerm"}, "WezTerm", osc9},
		{map[string]string{"TERM_PROGRAM": "ghostty"}, "Ghostty", osc777},
		{map[string]string{"TERM_PROGRAM": "WarpTerminal"}, "Warp", osc777},
		{map[string]string{"KITTY_WINDOW_ID": "1"}, "kitty", osc99},
		{map[string]string{"TERM": "xterm-kitty"}, "kitty", osc99},
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, "Apple_Terminal", ""},
		{map[string]string{}, "", ""},
	} {
		name, p := Terminal(func(k string) string { return tc.env[k] })
		if name != tc.name || p != tc.protocol {
			t.Errorf("%v: %q %q", tc.env, name, p)
		}
	}
}

func TestSequence(t *testing.T) {
	for _, tc := range []struct {
		protocol string
		sound    bool
		want     string
	}{
		{osc9, false, "\x1b]9;a·b: x\a"},
		{osc9, true, "\x1b]9;a·b: x\a\a"},
		{osc777, false, "\x1b]777;notify;a·b;x\a"},
		{osc99, false, "\x1b]99;i=squadrn:d=0;a·b\x1b\\\x1b]99;i=squadrn:d=1:p=body;x\x1b\\"},
		{"", true, ""},
	} {
		if got := Sequence(tc.protocol, "a·b", "x", tc.sound); got != tc.want {
			t.Errorf("%s: %q", tc.protocol, got)
		}
	}
	if got := Sequence(osc777, "t;\x1b]0;evil\a", "b\u009b", false); got != "\x1b]777;notify;t,]0,evil;b\a" {
		t.Errorf("unsafe text: %q", got)
	}
}
