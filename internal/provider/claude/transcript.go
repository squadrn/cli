package claude

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/provider"
)

// Claude Code stores each conversation at <config dir>/projects/<project folder>/<session>.jsonl,
// with subagent transcripts and tool results in a sibling <session>/ directory and file
// checkpoints in <config dir>/file-history/<session>/. The project folder encodes the working
// directory (long paths are shortened with a hash, by an undocumented rule), so sessions are
// found by ID and moved under the same folder name. This layout is internal to Claude Code; swap
// depends on it and the tests pin it.

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// projectKey encodes a working directory the way Claude Code names the project folders of short
// paths. Only the worktree lookup uses it; see latestSessionIn.
func projectKey(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return nonAlnum.ReplaceAllString(abs, "-"), nil
}

// Session's Path is the .jsonl transcript.
type Session = provider.Session

// ErrVerify: the copy does not match the source (size or SHA-256).
var ErrVerify = errors.New("copied conversation does not match the source")

// projectDir is the name of the project folder holding s, reused in the target account.
func projectDir(s Session) string { return filepath.Base(filepath.Dir(s.Path)) }

func (CLI) FindSession(configDirs []string, id string) (Session, error) {
	return findSession(configDirs, id)
}

func (CLI) LatestSessionIn(configDirs []string, dir string) (Session, error) {
	return latestSessionIn(configDirs, dir)
}

func (CLI) RunningSessions(configDirs []string, alive func(pid int) bool) []provider.LiveSession {
	return runningSessions(configDirs, alive)
}

func (CLI) SessionCwd(s Session) string { return transcriptCwd(s.Path) }

func (CLI) TransferSession(s Session, targetConfigDir string, force bool) error {
	return transferSession(s, targetConfigDir, force)
}

// UnavailableModel: Opus models are not part of the Pro plan.
func (CLI) UnavailableModel(s Session, target provider.AuthStatus) string {
	model := lastModel(s.Path)
	if strings.Contains(strings.ToLower(model), "opus") && strings.EqualFold(target.SubscriptionType, "pro") {
		return model
	}
	return ""
}

func sessionAt(path string, owner int) (Session, bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return Session{}, false
	}
	return Session{
		ID:      strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path:    path,
		ModTime: fi.ModTime(),
		Owner:   owner,
	}, true
}

// findSession finds conversation id in configDirs. When several accounts hold it (it was
// swapped before), the most recently written copy wins.
func findSession(configDirs []string, id string) (Session, error) {
	if !provider.Claude.SessionID.MatchString(id) {
		return Session{}, fmt.Errorf("%q: %w", id, provider.ErrBadSessionID)
	}
	s, err := latest(configDirs, func(dir string) []string {
		files, _ := filepath.Glob(filepath.Join(dir, "projects", "*", id+".jsonl"))
		return files
	})
	if err != nil {
		return Session{}, fmt.Errorf("session %s: %w", id, err)
	}
	return s, nil
}

// latestSession returns the most recently written conversation in project folder key across
// config dirs.
func latestSession(configDirs []string, key string) (Session, error) {
	return latest(configDirs, func(dir string) []string {
		files, _ := filepath.Glob(filepath.Join(dir, "projects", key, "*.jsonl"))
		return files
	})
}

// latestSessionIn returns the most recent conversation started in dir. The project folder is
// computed from dir; when no account has that folder (Claude Code shortens long paths), the
// transcripts whose first records name dir as their working directory are used instead.
func latestSessionIn(configDirs []string, dir string) (Session, error) {
	key, err := projectKey(dir)
	if err != nil {
		return Session{}, err
	}
	for _, c := range configDirs {
		if fsx.IsDir(filepath.Join(c, "projects", key)) {
			return latestSession(configDirs, key)
		}
	}
	return latest(configDirs, func(c string) []string {
		files, _ := filepath.Glob(filepath.Join(c, "projects", "*", "*.jsonl"))
		var out []string
		for _, f := range files {
			if cwd := transcriptCwd(f); cwd != "" && fsx.SamePath(cwd, dir) {
				out = append(out, f)
			}
		}
		return out
	})
}

func latest(configDirs []string, files func(dir string) []string) (Session, error) {
	var best Session
	found := false
	for i, dir := range configDirs {
		for _, f := range files(dir) {
			if s, ok := sessionAt(f, i); ok && (!found || s.ModTime.After(best.ModTime)) {
				best, found = s, true
			}
		}
	}
	if !found {
		return Session{}, provider.ErrNoSession
	}
	return best, nil
}

// headRecords is how many leading records are searched for the working directory: the first
// ones can be summaries or snapshots without it.
const headRecords = 50

// transcriptCwd returns the working directory the conversation was started in, from the first
// records that carry one; "" when none does.
func transcriptCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	for range headRecords {
		line, err := r.ReadBytes('\n')
		var rec struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Cwd != "" {
			return rec.Cwd
		}
		if err != nil {
			return ""
		}
	}
	return ""
}

// lastModel returns the model of the last assistant record in the conversation; "" when none.
func lastModel(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	model := ""
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, []byte(`"assistant"`)) {
			var rec struct {
				Type    string `json:"type"`
				Message struct {
					Model string `json:"model"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &rec) == nil && rec.Type == "assistant" && rec.Message.Model != "" {
				model = rec.Message.Model
			}
		}
		if err != nil {
			return model
		}
	}
}

// configDirOf is the config dir holding a conversation file (<config>/projects/<folder>/x.jsonl).
func configDirOf(s Session) string { return filepath.Dir(filepath.Dir(filepath.Dir(s.Path))) }

// stageHook lets tests alter a staged copy before it is verified.
var stageHook func(path string)

// transferSession copies conversation s into targetConfigDir under the same project folder, with
// its <id>/ directory and its file checkpoints, so that account can resume it. When the target
// already holds the conversation, the source must have grown from it (append only) unless
// force is set; otherwise provider.ErrDiverged. The transcript is staged next to its destination,
// checked against the source (size and SHA-256) and renamed into place last; the directories
// are replaced through a staged copy. Times are kept so "latest" stays meaningful.
func transferSession(s Session, targetConfigDir string, force bool) error {
	srcConfig := configDirOf(s)
	dstDir := filepath.Join(targetConfigDir, "projects", projectDir(s))
	dst := filepath.Join(dstDir, s.ID+".jsonl")
	if fsx.SameFile(s.Path, dst) {
		return nil
	}
	if !force {
		if err := appendOnly(s.Path, dst); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		return err
	}
	for _, d := range [][2]string{
		{filepath.Join(filepath.Dir(s.Path), s.ID), filepath.Join(dstDir, s.ID)},
		{filepath.Join(srcConfig, "file-history", s.ID), filepath.Join(targetConfigDir, "file-history", s.ID)},
	} {
		if !fsx.IsDir(d[0]) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(d[1]), 0o700); err != nil {
			return err
		}
		fsx.CleanStale(d[1])
		if err := fsx.ReplaceDir(d[0], d[1], 0o700); err != nil {
			return err
		}
	}
	return copyVerified(s.Path, dst)
}

// appendOnly checks that dst is missing or a prefix of src.
func appendOnly(src, dst string) error {
	d, err := os.Open(dst)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = sf.Close() }()
	a, b := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		n, derr := io.ReadFull(d, a)
		if n > 0 {
			m, _ := io.ReadFull(sf, b[:n])
			if m < n || !bytes.Equal(a[:n], b[:n]) {
				return provider.ErrDiverged
			}
		}
		if derr == io.EOF || derr == io.ErrUnexpectedEOF {
			return nil
		} else if derr != nil {
			return derr
		}
	}
}

func copyVerified(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".squadrn-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), in)
	if err != nil {
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if stageHook != nil {
		stageHook(tmp.Name())
	}
	size, sum, err := digest(tmp.Name())
	if err != nil {
		return err
	}
	if size != n || !bytes.Equal(sum, h.Sum(nil)) {
		return fmt.Errorf("%s: %w", dst, ErrVerify)
	}
	if now, err := os.Stat(src); err != nil || now.Size() != n {
		return fmt.Errorf("%s changed while it was copied: %w", src, ErrVerify)
	}
	if err := os.Chtimes(tmp.Name(), fi.ModTime(), fi.ModTime()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func digest(path string) (int64, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return n, h.Sum(nil), err
}
