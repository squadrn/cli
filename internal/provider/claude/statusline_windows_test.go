//go:build windows

package claude

import "testing"

func TestStatusLineWordWindows(t *testing.T) {
	home := `C:\Users\Jane Doe`
	tests := []struct {
		in, want string
		ok       bool
	}{
		{`C:\Users\Jane Doe\.local\share\squadrn\bin\squadrn.exe`, "~/.local/share/squadrn/bin/squadrn.exe", true},
		{`D:\tools\squadrn.exe`, "D:/tools/squadrn.exe", true},
		{`D:\my tools\squadrn.exe`, "", false},
		{"powershell -NoProfile -File C:/sl.ps1", "'powershell -NoProfile -File C:/sl.ps1'", true},
		{"echo it's", "", false},
	}
	for _, tt := range tests {
		got, err := statusLineWord(tt.in, home)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("%q: got %q, %v; want %q", tt.in, got, err, tt.want)
		}
		if tt.ok {
			if old, ours := UnwrapStatusLine("x hook statusline --then "+got, "hook statusline"); !ours || (old != tt.in && old != tt.want) {
				t.Errorf("%q: unwrap = %q %v", tt.in, old, ours)
			}
		}
	}
	if sh, args := StatusLineShell("true"); sh == "" || len(args) != 2 {
		t.Errorf("shell = %s %v", sh, args)
	}
}
