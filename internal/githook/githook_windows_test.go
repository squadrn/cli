//go:build windows

package githook

import (
	"slices"
	"strings"
	"testing"
)

func TestHookCommandWindows(t *testing.T) {
	if bin, args := HookCommand(`C:\hooks\pre-push.exe`, []string{"a"}); bin != `C:\hooks\pre-push.exe` || !slices.Equal(args, []string{"a"}) {
		t.Fatalf("exe hook: %s %v", bin, args)
	}
	sh := GitShell()
	if sh == "" || !strings.HasSuffix(strings.ToLower(sh), "sh.exe") {
		t.Fatalf("GitShell = %q (Git for Windows is installed on the runner)", sh)
	}
	if bin, args := HookCommand(`C:\hooks\pre-push`, []string{"a"}); bin != sh || !slices.Equal(args, []string{`C:\hooks\pre-push`, "a"}) {
		t.Fatalf("script hook: %s %v", bin, args)
	}
	t.Setenv("PATH", "")
	if GitShell() != "" {
		t.Fatal("GitShell without git on PATH")
	}
	if bin, _ := HookCommand(`C:\hooks\pre-push`, nil); bin != "sh.exe" {
		t.Fatalf("fallback shell = %q", bin)
	}
}
