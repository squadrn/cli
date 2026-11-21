package gemini

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/squadrn/squadrn/internal/fsx"
)

// Folder trust is per account: <state>/trustedFolders.json maps absolute paths to TRUST_FOLDER
// (the folder and everything below), TRUST_PARENT (the folder's parent and everything below) or
// DO_NOT_TRUST. squadrn never sets GEMINI_CLI_TRUST_WORKSPACE, which would also trust the hooks
// and MCP servers a repository brings.
const (
	trustFile   = "trustedFolders.json"
	trustFolder = "TRUST_FOLDER"
	trustParent = "TRUST_PARENT"
)

func (c CLI) readTrust(configDir string) (map[string]string, string, error) {
	path := filepath.Join(c.state(configDir), trustFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, path, nil
	} else if err != nil {
		return nil, path, err
	}
	rules := map[string]string{}
	if b, _ = stripComments(b); len(bytes.TrimSpace(b)) == 0 {
		return rules, path, nil
	}
	if err := json.Unmarshal(b, &rules); err != nil {
		return nil, path, fmt.Errorf("parse %s: %w", path, err)
	}
	return rules, path, nil
}

// Trusted reports whether a rule in the account's trust file decides about dir.
func (c CLI) Trusted(configDir, dir string) (bool, error) {
	rules, _, err := c.readTrust(configDir)
	if err != nil {
		return false, err
	}
	for p, level := range rules {
		root := p
		if level == trustParent {
			root = filepath.Dir(p)
		}
		if fsx.SamePath(root, dir) || fsx.Within(dir, root) {
			return true, nil
		}
	}
	return false, nil
}

// Trust records dir, and so every worktree under it, as trusted (TRUST_FOLDER: TRUST_PARENT
// would also trust dir's siblings).
func (c CLI) Trust(configDir, dir string) error {
	rules, path, err := c.readTrust(configDir)
	if err != nil {
		return err
	}
	rules[dir] = trustFolder
	b, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsx.WriteBytes(path, append(b, '\n'), 0o600)
}
