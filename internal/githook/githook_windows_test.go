//go:build windows

package githook

import (
	"os"
	"path/filepath"
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

func TestRootAbove(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{`cmd\git.exe`, `bin\git.exe`, `usr\bin\sh.exe`, `ucrt64\bin\git.exe`, `mingw64\bin\git.exe`, `mingw64\libexec\git-core\git.exe`} {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{`cmd\git.exe`, `bin\git.exe`, `ucrt64\bin\git.exe`, `mingw64\bin\git.exe`, `mingw64\libexec\git-core\git.exe`} {
		if got := rootAbove(filepath.Join(root, f)); got != root {
			t.Errorf("rootAbove(%s) = %q, want %q", f, got, root)
		}
	}
	shim := filepath.Join(t.TempDir(), "scoop", "shims", "git.exe")
	if got := rootAbove(shim); got != "" {
		t.Errorf("rootAbove(shim) = %q, want none", got)
	}
}

// Git Bash puts <env>\bin\git.exe first on PATH; the root and sh.exe are still found.
func TestGitRootFromGitBashPath(t *testing.T) {
	root := GitRoot()
	if root == "" {
		t.Fatal("GitRoot: Git for Windows is installed on the runner")
	}
	for _, env := range []string{"ucrt64", "mingw64", "clang64"} {
		dir := filepath.Join(root, env, "bin")
		if !isFile(filepath.Join(dir, "git.exe")) {
			continue
		}
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		if got := GitRoot(); got != root {
			t.Errorf("GitRoot with %s first = %q, want %q", dir, got, root)
		}
		if GitShell() == "" {
			t.Errorf("GitShell with %s first: none", dir)
		}
		return
	}
	t.Skip("no <env>\\bin\\git.exe in this Git for Windows")
}
