//go:build darwin

package account

import "path/filepath"

// trashDirs is the macOS trash, ~/.Trash (Finder's "Put Back" needs no .trashinfo). It is used
// even when it cannot be listed (privacy settings), since moving into it still works.
func trashDirs(home string) (files, info string) {
	return filepath.Join(home, ".Trash"), ""
}
