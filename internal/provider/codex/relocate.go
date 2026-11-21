package codex

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/provider"
)

// Relocate fixes a CODEX_HOME moved from oldDir to newDir. Codex keys hook trust by the hook's
// file path (hooks.state."<dir>/hooks.json:…" in config.toml); the keys are rewritten so the
// guard hook stays trusted.
func (CLI) Relocate(oldDir, newDir string) error {
	path := filepath.Join(newDir, provider.Codex.Settings)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// TOML basic strings escape like Go's: match the quoted key prefix as Codex writes it, with
	// the dir as given and with links resolved (Codex may canonicalise CODEX_HOME).
	key := func(dir string) string {
		return strings.TrimSuffix(strconv.Quote(filepath.Join(dir, hooksFile)+":"), `"`)
	}
	out := strings.ReplaceAll(string(b), key(oldDir), key(newDir))
	out = strings.ReplaceAll(out, key(fsx.Real(oldDir)), key(fsx.Real(newDir)))
	if out == string(b) {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return fsx.WriteBytes(path, []byte(out), fi.Mode().Perm())
}
