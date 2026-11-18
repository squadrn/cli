package claude

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"sprawling-agents/internal/fsx"
)

// Claude Code stores each conversation at <config dir>/projects/<project key>/<session>.jsonl,
// with subagent transcripts in a sibling <session>/ directory. This layout is internal to
// Claude Code; swap depends on it and the tests pin it.

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// ProjectKey encodes a working directory the way Claude Code names its project folders.
func ProjectKey(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return nonAlnum.ReplaceAllString(abs, "-"), nil
}

type Session struct {
	ID      string
	Path    string // .jsonl file
	ModTime time.Time
	// Owner is the index of the config dir (in the list given to LatestSession) holding it.
	Owner int
}

var ErrNoSession = errors.New("no conversation found")

// LatestSession returns the most recently written conversation for key across config dirs.
func LatestSession(configDirs []string, key string) (Session, error) {
	var best Session
	found := false
	for i, dir := range configDirs {
		files, _ := filepath.Glob(filepath.Join(dir, "projects", key, "*.jsonl"))
		for _, f := range files {
			fi, err := os.Stat(f)
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			if !found || fi.ModTime().After(best.ModTime) {
				best = Session{ID: strings.TrimSuffix(filepath.Base(f), ".jsonl"), Path: f, ModTime: fi.ModTime(), Owner: i}
				found = true
			}
		}
	}
	if !found {
		return Session{}, ErrNoSession
	}
	return best, nil
}

// CopySession copies a conversation (and its subagent directory) into another config dir so
// that account can resume it. Timestamps are kept so "latest" stays meaningful.
func CopySession(s Session, configDir, key string) error {
	dst := filepath.Join(configDir, "projects", key)
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	if err := fsx.CopyFile(s.Path, filepath.Join(dst, filepath.Base(s.Path)), 0o600, true); err != nil {
		return err
	}
	sub := strings.TrimSuffix(s.Path, ".jsonl")
	if fsx.IsDir(sub) {
		return fsx.CopyTree(sub, filepath.Join(dst, filepath.Base(sub)), 0o700, true)
	}
	return nil
}
