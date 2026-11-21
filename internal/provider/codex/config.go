package codex

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/provider"
)

// storeKey selects where Codex keeps credentials. "file" is auth.json in CODEX_HOME: the OS
// keyring ("keyring", or "auto" where one exists) would not follow the account's dir.
const (
	storeKey  = "cli_auth_credentials_store"
	storeFile = "file"
	storeLine = storeKey + ` = "` + storeFile + `"`
)

var (
	storeAssign = regexp.MustCompile(`^\s*` + storeKey + `\s*=`)
	tableHeader = regexp.MustCompile(`^\s*\[`)
)

// FileStore is the credentials store config.toml at path selects; "" when it sets none.
func FileStore(path string) (string, error) {
	doc, _, err := readConfig(path)
	if err != nil {
		return "", err
	}
	v, _ := doc[storeKey].(string)
	return v, nil
}

// EnsureFileStore sets cli_auth_credentials_store = "file" in config.toml at path, changing
// only that line (or adding it at the top) so the rest of the file stays as the user wrote it.
// A file that does not parse is left alone and reported.
func EnsureFileStore(path string) error {
	doc, raw, err := readConfig(path)
	if err != nil {
		return err
	}
	if v, _ := doc[storeKey].(string); v == storeFile {
		return nil
	}
	out := setStoreLine(raw)
	check := map[string]any{}
	if _, err := toml.Decode(out, &check); err != nil || check[storeKey] != storeFile {
		return fmt.Errorf("set %s in %s: the file has a form squadrn cannot edit; add %s at its top", storeKey, path, storeLine)
	}
	perm := fs.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	}
	return fsx.WriteBytes(path, []byte(out), perm)
}

func readConfig(path string) (map[string]any, string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, "", nil
	} else if err != nil {
		return nil, "", err
	}
	doc := map[string]any{}
	if _, err := toml.Decode(string(b), &doc); err != nil {
		return nil, "", fmt.Errorf("%s is not valid TOML: %w", path, err)
	}
	return doc, string(b), nil
}

// setStoreLine replaces the top-level assignment of storeKey, or inserts one before the first
// table.
func setStoreLine(raw string) string {
	lines := strings.SplitAfter(raw, "\n")
	for i, l := range lines {
		if tableHeader.MatchString(l) {
			break
		}
		if storeAssign.MatchString(l) {
			lines[i] = storeLine + "\n"
			return strings.Join(lines, "")
		}
	}
	return storeLine + "\n" + raw
}

// CheckCredentials reports an account whose config.toml does not keep credentials in its own
// auth.json.
func (CLI) CheckCredentials(configDir string) error {
	v, err := FileStore(filepath.Join(configDir, provider.Codex.Settings))
	switch {
	case err != nil:
		return err
	case v != storeFile:
		if v == "" {
			v = "unset"
		}
		return fmt.Errorf("%s is %s, not %q", storeKey, v, storeFile)
	}
	return nil
}
