package guard

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"

	"github.com/squadrn/cli/internal/config"
	"github.com/squadrn/cli/internal/proc"
)

// Facts is what the guard needs to know about one repository, read once per decision.
type Facts struct {
	NotRepo bool
	Branch  string // current branch; "" when detached
	// Config holds every effective git config entry (all scopes, includes not followed),
	// keys lower-cased as git prints them.
	Config map[string][]string
}

// Git answers repository questions. Errors (including deadlines) make the guard ask.
type Git interface {
	Facts(ctx context.Context, dir string) (Facts, error)
	// IsAncestor reports whether a is an ancestor of b; an unknown object is an error.
	IsAncestor(ctx context.Context, dir, a, b string) (bool, error)
}

// GitCLI queries the installed git. Callers bound every call with the context deadline.
type GitCLI struct{}

func (GitCLI) Facts(ctx context.Context, dir string) (Facts, error) {
	branch, err := proc.Output(ctx, proc.Options{}, "git", "-C", dir, "symbolic-ref", "--short", "-q", "HEAD")
	f := Facts{Branch: strings.TrimSpace(string(branch))}
	switch code := proc.ExitCode(err); {
	case err == nil:
	case code == 1:
		f.Branch = "" // detached HEAD
	case code == 128:
		return Facts{NotRepo: true}, nil
	default:
		return Facts{}, err
	}
	// --no-includes: an included file (or a FIFO posing as one) cannot stall or steer the guard.
	out, err := proc.Output(ctx, proc.Options{}, "git", "-C", dir, "config", "--list", "--null", "--no-includes")
	if err != nil && proc.ExitCode(err) != 1 {
		return Facts{}, err
	}
	f.Config = parseConfigList(out)
	return f, nil
}

func (GitCLI) IsAncestor(ctx context.Context, dir, a, b string) (bool, error) {
	_, err := proc.Output(ctx, proc.Options{}, "git", "-C", dir, "merge-base", "--is-ancestor", a, b)
	switch proc.ExitCode(err) {
	case -1:
		return err == nil, err
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, err
	}
}

// parseConfigList reads `git config --list --null`: key\nvalue\0 records.
func parseConfigList(out []byte) map[string][]string {
	cfg := map[string][]string{}
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		k, v, _ := strings.Cut(string(rec), "\n")
		k = strings.ToLower(k)
		cfg[k] = append(cfg[k], v)
	}
	return cfg
}

// factsCache memoises Facts per directory within one decision.
type factsCache struct {
	git Git
	mu  sync.Mutex
	m   map[string]factsResult
}

type factsResult struct {
	f   Facts
	err error
}

func newFactsCache(g Git) *factsCache {
	return &factsCache{git: g, m: map[string]factsResult{}}
}

func (c *factsCache) get(ctx context.Context, dir string) (Facts, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r, ok := c.m[dir]; ok {
		return r.f, r.err
	}
	if c.git == nil {
		return Facts{}, errors.New("no git available")
	}
	f, err := c.git.Facts(ctx, dir)
	c.m[dir] = factsResult{f, err}
	return f, err
}

// RepoConfigKey lets a repository add protected branches without committing a file.
const RepoConfigKey = config.Slug + ".protectedBranches"

// protectedSet combines the user's list with branches a repository adds. A repository can only
// add: an agent writing `git config squadrn.protectedBranches none` must not unprotect main.
func protectedSet(user []string, f Facts) branchMatcher {
	globs := append([]string(nil), user...)
	for _, v := range f.Config[strings.ToLower(RepoConfigKey)] {
		globs = append(globs, splitList(v)...)
	}
	return newBranchMatcher(globs)
}

// branchMatcher matches branch names case-insensitively against globs where * spans any
// characters, slashes included. Case-insensitive because hosts and case-insensitive file
// systems can treat Main and main as the same ref.
type branchMatcher []*regexp.Regexp

func newBranchMatcher(globs []string) branchMatcher {
	m := make(branchMatcher, 0, len(globs))
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		parts := strings.Split(g, "*")
		for i, p := range parts {
			parts[i] = regexp.QuoteMeta(p)
		}
		m = append(m, regexp.MustCompile("(?i)^"+strings.Join(parts, ".*")+"$"))
	}
	return m
}

func (m branchMatcher) match(branch string) bool {
	for _, re := range m {
		if re.MatchString(branch) {
			return true
		}
	}
	return false
}

// splitList accepts space, comma or pipe separated branch globs.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' || r == '|' || r == '\t' })
}
