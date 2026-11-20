//go:build windows

package githook

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/squadrn/squadrn/internal/proc"
)

// isRunnable mirrors Git for Windows: there are no execute bits, so a hook runs when it is a
// regular file that is an .exe or starts with "#!".
func isRunnable(path string, fi fs.FileInfo) bool {
	if fi.IsDir() || !fi.Mode().IsRegular() {
		return false
	}
	if strings.EqualFold(filepath.Ext(path), ".exe") {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 2)
	n, _ := f.Read(head)
	return bytes.Equal(head[:n], []byte("#!"))
}

// HookCommand starts the hook the way Git for Windows does: an .exe directly, a script through
// Git's own sh.exe, which reads the #! line.
func HookCommand(path string, args []string) (string, []string) {
	if strings.EqualFold(filepath.Ext(path), ".exe") {
		return path, args
	}
	sh := GitShell()
	if sh == "" {
		sh = "sh.exe"
		if p, err := exec.LookPath("sh"); err == nil {
			sh = p
		}
	}
	return sh, append([]string{path}, args...)
}

// GitShell finds sh.exe of the Git for Windows installation that provides git; "" if none.
func GitShell() string {
	root := GitRoot()
	if root == "" {
		return ""
	}
	for _, c := range []string{filepath.Join(root, "usr", "bin", "sh.exe"), filepath.Join(root, "bin", "sh.exe")} {
		if isFile(c) {
			return c
		}
	}
	return ""
}

// GitRoot is the Git for Windows installation that provides git on PATH ("" if none). git.exe
// is in cmd\ or bin\ of an install, or in <env>\bin\ (ucrt64, mingw64, …), which Git Bash puts
// first on PATH; any other git.exe (a scoop shim, say) is located through git --exec-path.
func GitRoot() string {
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	if root := rootAbove(git); root != "" {
		return root
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := proc.Output(ctx, proc.Options{}, git, "--exec-path")
	if err != nil {
		return ""
	}
	return rootAbove(filepath.Join(filepath.FromSlash(strings.TrimSpace(string(out))), "git.exe"))
}

// rootAbove finds the Git for Windows root at most three directories above git.exe: the one
// holding cmd\git.exe or usr\bin\sh.exe.
func rootAbove(git string) string {
	dir := filepath.Dir(git)
	for range 4 {
		if isFile(filepath.Join(dir, "cmd", "git.exe")) || isFile(filepath.Join(dir, "usr", "bin", "sh.exe")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
