//go:build !darwin

package account

import (
	"path/filepath"

	"github.com/squadrn/squadrn/internal/config"
)

// trashDirs is the freedesktop trash ($XDG_DATA_HOME/Trash): files, and the .trashinfo files
// desktop trash tools need to restore them.
func trashDirs(home string) (files, info string) {
	base := config.DirsFor(home, "Trash").Data
	return filepath.Join(base, "files"), filepath.Join(base, "info")
}
