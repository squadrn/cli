// Package githook is the git-level guard (layer 2). Sessions sprawl starts get core.hooksPath
// pointed at a directory of shims through GIT_CONFIG_* environment entries; each shim calls
// `sprawl hook git <name>`, which judges pushes (pre-push) and then runs the repository's own
// hook of the same name, so existing hooks (husky, lefthook, pre-commit) keep working.
package githook

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"sprawling-agents/internal/fsx"
	"sprawling-agents/internal/proc"
)

// Names are the hooks shimmed. Hooks git runs on hot paths (reference-transaction on every ref
// update, post-index-change, fsmonitor-watchman) are left out: a repository's own hook of those
// names does not run inside sprawl sessions.
var Names = []string{
	"applypatch-msg", "pre-applypatch", "post-applypatch",
	"pre-commit", "pre-merge-commit", "prepare-commit-msg", "commit-msg", "post-commit",
	"pre-rebase", "post-checkout", "post-merge", "pre-push", "post-rewrite",
	"pre-auto-gc", "push-to-checkout", "sendemail-validate", "post-update",
}

const marker = "# sprawl git-level guard"

// Dir is where the shims live inside sprawl's data dir.
func Dir(dataDir string) string { return filepath.Join(dataDir, "git-hooks") }

// Install writes one shim per hook name into dir, each calling binary. Files that are not
// sprawl shims are left alone.
func Install(dir, binary string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, n := range Names {
		script := fmt.Sprintf("#!/bin/sh\n%s: runs the push check, then the repository's own %s hook.\nexec %s hook git %s \"$@\"\n",
			marker, n, shellQuote(filepath.ToSlash(binary)), n)
		if err := fsx.WriteBytes(filepath.Join(dir, n), []byte(script), 0o755); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if slices.Contains(Names, e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), marker) {
			_ = os.Remove(p)
		}
	}
	return nil
}

// Installed reports whether dir holds current shims for binary.
func Installed(dir, binary string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "pre-push"))
	return err == nil && strings.Contains(string(b), shellQuote(filepath.ToSlash(binary))+" hook git pre-push")
}

// Entries returns the environment entries that add core.hooksPath=dir after the GIT_CONFIG_*
// entries already in environ (the user's own are kept). nil means dir is already set.
func Entries(environ []string, dir string) []string {
	get := func(k string) (string, bool) {
		for i := len(environ) - 1; i >= 0; i-- {
			if v, ok := strings.CutPrefix(environ[i], k+"="); ok {
				return v, true
			}
		}
		return "", false
	}
	n := 0
	if v, ok := get("GIT_CONFIG_COUNT"); ok {
		if c, err := strconv.Atoi(v); err == nil && c > 0 {
			n = c
		}
	}
	for i := range n {
		k, _ := get("GIT_CONFIG_KEY_" + strconv.Itoa(i))
		v, _ := get("GIT_CONFIG_VALUE_" + strconv.Itoa(i))
		if strings.EqualFold(k, "core.hooksPath") && v == dir {
			return nil
		}
	}
	idx := strconv.Itoa(n)
	return []string{
		"GIT_CONFIG_COUNT=" + strconv.Itoa(n+1),
		"GIT_CONFIG_KEY_" + idx + "=core.hooksPath",
		"GIT_CONFIG_VALUE_" + idx + "=" + dir,
	}
}

// Env returns environ with Entries applied.
func Env(environ []string, dir string) []string {
	add := Entries(environ, dir)
	if add == nil {
		return environ
	}
	out := make([]string, 0, len(environ)+len(add))
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") {
			out = append(out, kv)
		}
	}
	return append(out, add...)
}

// RepoHook returns the repository's own hook called name, as git would run it without sprawl:
// the last core.hooksPath that is not ours, else <common git dir>/hooks. "" means there is none.
// dir is the directory git runs the hook in (the worktree top level, or the git dir if bare).
func RepoHook(ctx context.Context, dir, ours, name string) (string, error) {
	out, err := proc.Output(ctx, proc.Options{Dir: dir}, "git", "config", "--get-all", "core.hooksPath")
	if err != nil && proc.ExitCode(err) != 1 {
		return "", err
	}
	hooks := ""
	for _, v := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if v != "" && !samePath(expandHome(v), ours) {
			hooks = expandHome(v)
		}
	}
	if hooks == "" {
		common, err := proc.Output(ctx, proc.Options{Dir: dir}, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return "", err
		}
		hooks = filepath.Join(strings.TrimSpace(string(common)), "hooks")
	} else if !filepath.IsAbs(hooks) {
		hooks = filepath.Join(dir, hooks)
	}
	p := filepath.Join(hooks, name)
	fi, err := os.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", err
	case !isRunnable(p, fi):
		return "", nil // git skips hooks it would not run either
	}
	if samePath(filepath.Dir(p), ours) {
		return "", nil
	}
	return p, nil
}

// HookCommand is how git would start the hook at path with args.
func HookCommand(path string, args []string) (string, []string) { return hookCommand(path, args) }

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
