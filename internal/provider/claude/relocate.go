package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/squadrn/cli/internal/fsx"
)

// Relocate fixes a config dir that was moved from oldDir to newDir. Claude Code records
// absolute paths under the config dir in its plugin registry (plugins/*.json: install paths,
// marketplace locations); they are rewritten so plugins keep loading.
func (CLI) Relocate(oldDir, newDir string) error {
	files, err := filepath.Glob(filepath.Join(newDir, "plugins", "*.json"))
	if err != nil {
		return err
	}
	// Match the opening quote and the path prefix of a JSON string value, followed by either
	// separator (Claude Code writes \\ on Windows).
	var pairs [][2][]byte
	for _, sep := range []string{"/", `\`} {
		from, err := json.Marshal(oldDir + sep)
		if err != nil {
			return err
		}
		to, err := json.Marshal(newDir + sep)
		if err != nil {
			return err
		}
		pairs = append(pairs, [2][]byte{from[:len(from)-1], to[:len(to)-1]})
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		out := b
		for _, p := range pairs {
			out = bytes.ReplaceAll(out, p[0], p[1])
		}
		if bytes.Equal(out, b) {
			continue
		}
		fi, err := os.Stat(f)
		if err != nil {
			return err
		}
		if err := fsx.WriteBytes(f, out, fi.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}
