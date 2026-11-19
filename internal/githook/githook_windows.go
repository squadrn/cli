//go:build windows

package githook

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	root := filepath.Dir(filepath.Dir(git)) // …\Git\cmd\git.exe or …\Git\bin\git.exe
	for _, c := range []string{filepath.Join(root, "usr", "bin", "sh.exe"), filepath.Join(root, "bin", "sh.exe")} {
		if fi, err := os.Stat(c); err == nil && fi.Mode().IsRegular() {
			return c
		}
	}
	return ""
}
