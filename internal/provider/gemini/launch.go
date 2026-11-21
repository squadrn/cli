package gemini

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/squadrn/squadrn-cli/internal/fsx"
)

// bundleRel is the CLI's entry point inside the npm package dir.
var bundleRel = filepath.Join("@google", "gemini-cli", "bundle", "gemini.js")

// shimTarget is the script path in an npm cmd-shim ("%dp0%\node_modules\…\gemini.js").
var shimTarget = regexp.MustCompile(`(?i)%~?dp0%?\\?([^"%\r\n]*node_modules[^"%\r\n]*\.js)`)

// bundleFor finds bundle/gemini.js of the npm package behind the gemini shim npm installed at
// shim (gemini.cmd on Windows): the script the shim names, else the package under the shim's
// node_modules (Windows prefix layout) or ../lib/node_modules (Unix prefix layout). "" when
// there is none.
func bundleFor(shim string) string {
	dir := filepath.Dir(shim)
	if b, err := os.ReadFile(shim); err == nil && len(b) < 64<<10 {
		if m := shimTarget.FindSubmatch(b); m != nil {
			p := filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(string(m[1]), `\`, "/")))
			if fsx.IsFile(p) {
				return p
			}
		}
	}
	for _, root := range []string{filepath.Join(dir, "node_modules"), filepath.Join(dir, "..", "lib", "node_modules")} {
		if p := filepath.Join(root, bundleRel); fsx.IsFile(p) {
			return filepath.Clean(p)
		}
	}
	return ""
}
