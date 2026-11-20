// Package account manages agent accounts on one machine. An account is a provider's config dir,
// referred to as provider/name: claude/main is ~/.claude, claude/personal is ~/.claude-personal.
package account

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/provider"
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

// Commands are squadrn's top-level subcommands; they cannot be account names, since
// `squadrn <word> ...` runs the subcommand. A provider CLI's subcommands can: an account of that
// name is matched before the subcommand is passed through (ADR 0011).
var Commands = []string{
	"account", "completion", "config", "deps", "doctor", "focus", "gc", "grid", "help", "hook", "next",
	"notify", "ps", "self-install", "self-uninstall", "self-update", "setup", "swap", "usage", "version", "wt",
}

func reserved(name string) bool { return slices.Contains(Commands, name) }

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
func (a Account) Label() string { return a.String() }

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
		return fmt.Errorf("%w %q: use 2 to 24 lower-case letters, digits and hyphens, starting with a letter", ErrInvalidName, name)
	}
	if reserved(name) {
		return fmt.Errorf("%w %q: it is a squadrn command", ErrReserved, name)
	}
	return nil
}

// MainError refuses to remove a provider's main account; it counts as ErrReserved.
type MainError struct{ Account Account }

func (e MainError) Error() string {
	return fmt.Sprintf("%s is the main account (~/%s) and cannot be removed", e.Account.Label(), filepath.ToSlash(e.Account.Spec.MainDir))
}
func (e MainError) Is(target error) bool { return target == ErrReserved }

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

// Marker marks a config dir as an account squadrn manages. Other tools use the same naming
// (~/.claude-<x>), so a matching dir alone is not an account. Main accounts need none.
const Marker = "." + config.Slug + "-account"

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

// mcpConfig is the provider's main-account file holding user-scope MCP servers.
func (s Store) mcpConfig(spec provider.Spec) string { return filepath.Join(s.Home, spec.MCPConfig) }

func (s Store) account(spec provider.Spec, name string) Account {
	dir := filepath.Join(s.Home, spec.DirPrefix+name)
	if name == Main {
		dir = s.MainDir(spec)
	}
	return Account{Ref: Ref{Provider: spec.ID, Name: name}, Spec: spec, Dir: dir}
}

// exists: the default provider's main always exists; another main needs its dir; every other
// account needs its dir with Marker.
func (s Store) exists(a Account) bool {
	if a.IsMain() {
		return a.Provider == s.defaultProvider() || fsx.IsDir(a.Dir)
	}
	return fsx.IsDir(a.Dir) && fsx.IsFile(filepath.Join(a.Dir, Marker))
}

// Mark records a's dir as managed by squadrn.
func Mark(a Account) error {
	if a.IsMain() {
		return nil
	}
	return fsx.WriteBytes(filepath.Join(a.Dir, Marker), []byte("This directory is an account managed by Squadrn.\n"), 0o600)
}

// Unmanaged lists dirs named like accounts (~/.claude-<name>) that are not accounts: created by
// another tool, or by hand. Adopt turns one into an account.
func (s Store) Unmanaged() ([]Account, error) {
	var out []Account
	for _, spec := range s.providers() {
		names, err := s.candidates(spec)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if a := s.account(spec, n); !s.exists(a) {
				out = append(out, a)
			}
		}
	}
	return out, nil
}

// IsUnmanaged reports whether input names a dir like an account's (~/.claude-<name>) that is not
// an account: one Adopt would take over.
func (s Store) IsUnmanaged(input string) bool {
	a, err := s.Define(input)
	return err == nil && fsx.IsDir(a.Dir) && !s.exists(a)
}

// Adopt makes an existing dir named like an account an account (writes Marker).
func (s Store) Adopt(input string) (Account, error) {
	a, err := s.Define(input)
	if err != nil {
		return Account{}, err
	}
	if !fsx.IsDir(a.Dir) {
		return Account{}, NotFoundError{a.Ref}
	}
	return a, Mark(a)
}

// ByConfigDir is the account of spec whose config dir is dir, as the provider's ConfigEnv names
// it; "" is the main account.
func (s Store) ByConfigDir(spec provider.Spec, dir string) (Account, bool) {
	accts, err := s.List()
	if err != nil {
		return Account{}, false
	}
	for _, a := range accts {
		if a.Provider == spec.ID && (dir == "" && a.IsMain() || dir != "" && fsx.SamePath(a.Dir, dir)) {
			return a, true
		}
	}
	return Account{}, false
}

// Managed reports whether a's dir exists and is an account (false for an unmanaged dir).
func (s Store) Managed(a Account) bool { return s.exists(a) }

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

// Exists reports whether input names an existing account; used to route `squadrn <account> ...`.
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
		names, err := s.candidates(spec)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if a := s.account(spec, n); s.exists(a) {
				out = append(out, a)
			}
		}
	}
	return out, nil
}

// candidates are the sorted names of dirs named like spec's secondary accounts.
func (s Store) candidates(spec provider.Spec) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(s.Home, spec.DirPrefix+"*"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, m := range matches {
		name := strings.TrimPrefix(filepath.Base(m), spec.DirPrefix)
		if fsx.IsDir(m) && ValidateName(name) == nil && name != Main {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// MoveToTrash moves a secondary account dir into the user's trash, keeping it recoverable. In
// the freedesktop trash it also writes the .trashinfo file desktop trash tools need to restore it.
func (s Store) MoveToTrash(a Account, now time.Time) (string, error) {
	if a.IsMain() {
		return "", MainError{a}
	}
	files, info := trashDirs(s.Home)
	if err := os.MkdirAll(files, 0o700); err != nil {
		return "", err
	}
	name := filepath.Base(a.Dir) + "-" + now.Format("20060102-150405")
	dst := filepath.Join(files, name)
	infoFile := ""
	if info != "" {
		if err := os.MkdirAll(info, 0o700); err != nil {
			return "", err
		}
		infoFile = filepath.Join(info, name+".trashinfo")
		u := url.URL{Path: filepath.ToSlash(a.Dir)}
		body := "[Trash Info]\nPath=" + u.EscapedPath() + "\nDeletionDate=" + now.Format("2006-01-02T15:04:05") + "\n"
		if err := fsx.WriteBytes(infoFile, []byte(body), 0o600); err != nil {
			return "", err
		}
	}
	if err := os.Rename(a.Dir, dst); err != nil {
		if infoFile != "" {
			_ = os.Remove(infoFile)
		}
		return "", fmt.Errorf("move %s to trash: %w", a.Dir, err)
	}
	return dst, nil
}

// RenameTarget checks that a can be renamed to newName and returns the account it would become.
func (s Store) RenameTarget(a Account, newName string) (Account, error) {
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
	return to, nil
}

// Rename moves a secondary account to a new name. The CLI keys its stored login to the config
// dir, so the account usually has to log in again.
func (s Store) Rename(a Account, newName string) (Account, error) {
	to, err := s.RenameTarget(a, newName)
	if err != nil {
		return Account{}, err
	}
	if err := os.Rename(a.Dir, to.Dir); err != nil {
		return Account{}, err
	}
	return to, nil
}
