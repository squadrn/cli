//go:build windows

package doctor

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// executable: Windows has no execute bit; the extension decides (PATHEXT).
func executable(path string, fi fs.FileInfo) bool {
	if fi.IsDir() {
		return false
	}
	ext := strings.ToUpper(filepath.Ext(path))
	for _, e := range filepath.SplitList(pathext()) {
		if ext == strings.ToUpper(e) {
			return true
		}
	}
	return false
}

// private: access is governed by the profile directory's ACL, not mode bits.
func private(fs.FileInfo) bool { return true }

func pathext() string {
	if v := os.Getenv("PATHEXT"); v != "" {
		return v
	}
	return ".COM;.EXE;.BAT;.CMD"
}

// onPath lists distinct executables named name (with any PATHEXT extension) across PATH.
func onPath(name string) []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		for _, ext := range filepath.SplitList(pathext()) {
			p := filepath.Join(dir, name+strings.ToLower(ext))
			fi, err := os.Stat(p)
			if err != nil || !executable(p, fi) {
				continue
			}
			if real := strings.ToLower(resolve(p)); !seen[real] {
				seen[real] = true
				out = append(out, p)
			}
		}
	}
	return out
}
