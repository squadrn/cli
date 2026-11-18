package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"sprawling-agents/internal/fsx"
)

// Relocate fixes a config dir that was moved from oldDir to newDir. Claude Code records
// absolute paths under the config dir in its plugin registry (plugins/*.json: install paths,
// marketplace locations); they are rewritten so plugins keep loading.
func (CLI) Relocate(oldDir, newDir string) error {
	files, err := filepath.Glob(filepath.Join(newDir, "plugins", "*.json"))
	if err != nil {
		return err
	}
	from, err := json.Marshal(oldDir + "/")
	if err != nil {
		return err
	}
	to, err := json.Marshal(newDir + "/")
	if err != nil {
		return err
	}
	// Match the opening quote and the path prefix of a JSON string value.
	from, to = from[:len(from)-1], to[:len(to)-1]
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if !bytes.Contains(b, from) {
			continue
		}
		fi, err := os.Stat(f)
		if err != nil {
			return err
		}
		if err := fsx.WriteBytes(f, bytes.ReplaceAll(b, from, to), fi.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}
