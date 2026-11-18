// Package account manages agent accounts on one machine. An account is a provider's config dir,
// referred to as provider/name: claude/main is ~/.claude, claude/personal is ~/.claude-personal.
package account

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"sprawling-agents/internal/config"
	"sprawling-agents/internal/provider"
)

const Main = "main"

var (
	ErrInvalidName     = errors.New("invalid account name")
	ErrReserved        = errors.New("reserved account name")
	ErrNotFound        = errors.New("account not found")
	ErrAmbiguous       = errors.New("ambiguous account name")
	ErrUnknownProvider = errors.New("unknown provider")
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,23}$`)

// Commands are sprawl's top-level subcommands. They, and every provider CLI's subcommands, cannot
// be account names: `sprawl <word> ...` would be ambiguous.
var Commands = []string{
	"account", "completion", "config", "deps", "doctor", "gc", "grid", "help", "hook", "ps",
	"self-install", "self-uninstall", "self-update", "swap", "version", "wt",
}

func reserved(name string) bool {
	if slices.Contains(Commands, name) {
		return true
	}
	for _, spec := range provider.All() {
		if slices.Contains(spec.Subcommands, name) {
			return true
		}
	}
	return false
}

// Ref names an account: provider/name.
type Ref struct {
	Provider string
	Name     string
}

func (r Ref) String() string { return r.Provider + "/" + r.Name }

type Account struct {
	Ref
	Spec provider.Spec
	Dir  string
}

func (a Account) IsMain() bool { return a.Name == Main }

// Label is how the account is shown and can be typed: claude/personal.
func (a Account) Label() string { return a.Provider + "/" + a.Name }

// ConfigDir is the value for Spec.ConfigEnv; "" for main, which must run with it unset.
func (a Account) ConfigDir() string {
	if a.IsMain() {
		return ""
	}
	return a.Dir
}

// parseRef splits "provider/name"; the provider part is optional.
func parseRef(input string) (providerID, name string) {
	providerID, name, ok := strings.Cut(input, "/")
	if !ok {
		return "", input
	}
	return providerID, name
}

func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%w %q (expected %s)", ErrInvalidName, name, namePattern)
	}
	if reserved(name) {
		return fmt.Errorf("%w %q: it is a sprawl or provider subcommand", ErrReserved, name)
	}
	return nil
}

// NotFoundError carries the ref so callers can suggest how to create it.
type NotFoundError struct{ Ref Ref }

func (e NotFoundError) Error() string        { return "account not found: " + e.Ref.String() }
func (e NotFoundError) Is(target error) bool { return target == ErrNotFound }

type Store struct {
	Home string
	// DefaultProvider owns the "main" shorthand and is the provider for new bare names.
	DefaultProvider string
	// Providers overrides the registry (tests); nil means provider.All().
	Providers []provider.Spec
}

func (s Store) providers() []provider.Spec {
	if s.Providers != nil {
		return s.Providers
	}
	return provider.All()
}

func (s Store) provider(id string) (provider.Spec, error) {
	for _, p := range s.providers() {
		if p.ID == id {
			return p, nil
		}
	}
	ids := make([]string, 0, len(s.providers()))
	for _, p := range s.providers() {
		ids = append(ids, p.ID)
	}
	return provider.Spec{}, fmt.Errorf("%w %q (known: %s)", ErrUnknownProvider, id, strings.Join(ids, ", "))
}

func NewStore() (Store, error) {
	home, err := config.Home()
	if err != nil {
		return Store{}, err
	}
	return Store{Home: home, DefaultProvider: provider.Default}, nil
}

// MainDir is the main account dir of a provider (~/.claude for claude).
func (s Store) MainDir(spec provider.Spec) string { return filepath.Join(s.Home, spec.MainDir) }

// MCPConfig is the provider's main-account file holding user-scope MCP servers.
func (s Store) MCPConfig(spec provider.Spec) string { return filepath.Join(s.Home, spec.MCPConfig) }

func (s Store) account(spec provider.Spec, name string) Account {
	dir := filepath.Join(s.Home, spec.DirPrefix+name)
	if name == Main {
		dir = s.MainDir(spec)
	}
	return Account{Ref: Ref{Provider: spec.ID, Name: name}, Spec: spec, Dir: dir}
}

// exists: the default provider's main always exists; every other account needs its dir.
func (s Store) exists(a Account) bool {
	return (a.IsMain() && a.Provider == s.defaultProvider()) || isDir(a.Dir)
}

func (s Store) defaultProvider() string {
	if s.DefaultProvider == "" {
		return provider.Default
	}
	return s.DefaultProvider
}

// Get resolves an existing account. A bare name is looked up across providers and must be unique.
func (s Store) Get(input string) (Account, error) {
	pid, name := parseRef(input)
	if err := ValidateName(name); err != nil {
		return Account{}, err
	}
	if pid == "" && name == Main {
		pid = s.defaultProvider()
	}
	if pid != "" {
		spec, err := s.provider(pid)
		if err != nil {
			return Account{}, err
		}
		a := s.account(spec, name)
		if !s.exists(a) {
			return Account{}, NotFoundError{a.Ref}
		}
		return a, nil
	}
	var found []Account
	for _, spec := range s.providers() {
		if a := s.account(spec, name); s.exists(a) {
			found = append(found, a)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return Account{}, NotFoundError{Ref{Provider: s.defaultProvider(), Name: name}}
	default:
		labels := make([]string, len(found))
		for i, a := range found {
			labels[i] = a.Label()
		}
		return Account{}, fmt.Errorf("%w %q: use one of %s", ErrAmbiguous, name, strings.Join(labels, ", "))
	}
}

// Exists reports whether input names an existing account; used to route `sprawl <account> ...`.
func (s Store) Exists(input string) bool {
	_, err := s.Get(input)
	return err == nil
}

// Define validates a ref for a new (or existing) secondary account without requiring it to exist.
func (s Store) Define(input string) (Account, error) {
	pid, name := parseRef(input)
	if pid == "" {
		pid = s.defaultProvider()
	}
	spec, err := s.provider(pid)
	if err != nil {
		return Account{}, err
	}
	if err := ValidateName(name); err != nil {
		return Account{}, err
	}
	if name == Main {
		return Account{}, fmt.Errorf("%w %q: it is the provider's default account (~/%s)", ErrReserved, name, spec.MainDir)
	}
	return s.account(spec, name), nil
}

// List returns, per provider, main first and then every valid named account sorted by name.
func (s Store) List() ([]Account, error) {
	var out []Account
	for _, spec := range s.providers() {
		if main := s.account(spec, Main); s.exists(main) {
			out = append(out, main)
		}
		matches, err := filepath.Glob(filepath.Join(s.Home, spec.DirPrefix+"*"))
		if err != nil {
			return nil, err
		}
		var names []string
		for _, m := range matches {
			name := strings.TrimPrefix(filepath.Base(m), spec.DirPrefix)
			if isDir(m) && ValidateName(name) == nil && name != Main {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, s.account(spec, n))
		}
	}
	return out, nil
}

// MoveToTrash moves a secondary account dir into the user's trash, keeping it recoverable.
func (s Store) MoveToTrash(a Account, now time.Time) (string, error) {
	if a.IsMain() {
		return "", fmt.Errorf("%w %q: refusing to remove a provider's default account", ErrReserved, a.Label())
	}
	trash := TrashDir(s.Home)
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(trash, filepath.Base(a.Dir)+"-"+now.Format("20060102-150405"))
	if err := os.Rename(a.Dir, dst); err != nil {
		return "", fmt.Errorf("move %s to trash: %w", a.Dir, err)
	}
	return dst, nil
}

// Rename moves a secondary account to a new name. The CLI keys its stored login to the config
// dir, so the account usually has to log in again.
func (s Store) Rename(a Account, newName string) (Account, error) {
	if a.IsMain() {
		return Account{}, fmt.Errorf("%w %q: a provider's default account cannot be renamed", ErrReserved, a.Label())
	}
	to, err := s.Define(a.Provider + "/" + newName)
	if err != nil {
		return Account{}, err
	}
	if _, err := os.Lstat(to.Dir); err == nil {
		return Account{}, fmt.Errorf("account %s already exists (%s)", to.Label(), to.Dir)
	}
	if err := os.Rename(a.Dir, to.Dir); err != nil {
		return Account{}, err
	}
	return to, nil
}

// TrashDir is ~/.Trash on macOS and the freedesktop trash elsewhere.
func TrashDir(home string) string {
	if mac := filepath.Join(home, ".Trash"); isDir(mac) {
		return mac
	}
	return filepath.Join(config.DirsFor(home, "Trash").Data, "files")
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
