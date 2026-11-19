package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/squadrn/cli/internal/config"
)

// secretPatterns mask credentials that commonly appear in agent commands before they are logged.
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// URL userinfo up to the last @ before the host (passwords may contain @ and /).
	{regexp.MustCompile(`(://)[^\s'"]*@`), "${1}***@"},
	// Known token formats.
	{regexp.MustCompile(`\b(gh[pousr]_|github_pat_|glpat-|gl[a-z]{2,4}-|xox[abprs]-|npm_|sk-(?:ant-|proj-)?|hf_|pypi-)[A-Za-z0-9_\-]{8,}`), "${1}***"},
	{regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`), "${1}***"},
	// --token VALUE, --password "a b"
	{regexp.MustCompile(`(?i)(--?[a-z0-9-]*(?:token|password|passwd|secret|api[_-]?key|access[_-]?key)[a-z0-9-]*)(\s+)("[^"]*"|'[^']*'|\S+)`), "${1}${2}***"},
	// ANY_NAME_TOKEN=…, password: …, Authorization: Bearer … (the scheme is masked with the value)
	{regexp.MustCompile(`(?i)([A-Za-z0-9_.-]*(?:token|password|passwd|secret|api[_-]?key|access[_-]?key|authorization|credentials?))(\s*[=:]\s*)("[^"]*"|'[^']*'|(?:(?:bearer|basic|token)\s+)?[^\s"']+)`), "${1}${2}***"},
	{regexp.MustCompile(`(?i)\b(bearer\s+)[^\s"']+`), "${1}***"},
}

// Mask hides credentials in a command line.
func Mask(cmd string) string {
	for _, p := range secretPatterns {
		cmd = p.re.ReplaceAllString(cmd, p.repl)
	}
	return cmd
}

// Log appends guard decisions to a TSV file: time, verdict, layer, cwd, reason, masked command.
type Log struct {
	Path string
}

// DefaultLogPath is $XDG_STATE_HOME/sprawl/guard.log.
func DefaultLogPath(home string) string {
	return filepath.Join(config.DirsFor(home, config.Slug).State, "guard.log")
}

// Record is best-effort: logging must never change the decision or fail the hook.
func (l Log) Record(now time.Time, layer string, d Decision, cwd, cmd string) {
	dir := filepath.Dir(l.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.Chmod(dir, 0o700)
	f, err := openLog(l.Path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	oneLine := strings.NewReplacer("\n", "⏎", "\t", " ")
	_, _ = fmt.Fprintf(f, "%s\t%s\t%s\t%s\t%s\t%s\n",
		now.Format(time.RFC3339), d.Verdict, layer, cwd, oneLine.Replace(d.Reason), oneLine.Replace(Mask(cmd)))
}
