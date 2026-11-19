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

	"github.com/squadrn/cli/internal/config"
	"github.com/squadrn/cli/internal/fsx"
	"github.com/squadrn/cli/internal/provider"
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
	"self-install", "self-uninstall", "self-update", "swap", "usage", "version", "wt",
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
	// HasPlugin reports whether the sprawl plugin is installed in a config dir. Dirs sprawl
	// provisioned before it wrote Marker are recognised by it; nil adopts nothing.
	HasPlugin func(spec provider.Spec, dir string) bool
}

// Marker marks a config dir as an account sprawl manages. Other tools use the same naming
// (~/.claude-<x>), so a matching dir alone is not an account. Main accounts need none.
const Marker = ".sprawl-account"

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

// exists: the default provider's main always exists; another main needs its dir; every other
// account needs its dir with Marker (written now for dirs sprawl provisioned before markers).
func (s Store) exists(a Account) bool {
	switch {
	case a.IsMain():
		return a.Provider == s.defaultProvider() || isDir(a.Dir)
	case !isDir(a.Dir):
		return false
	case isFile(filepath.Join(a.Dir, Marker)):
		return true
	case s.HasPlugin != nil && s.HasPlugin(a.Spec, a.Dir):
		return Mark(a) == nil
	}
	return false
}

// Mark records a's dir as managed by sprawl.
func Mark(a Account) error {
	if a.IsMain() {
		return nil
	}
	return fsx.WriteBytes(filepath.Join(a.Dir, Marker), []byte("This directory is an account managed by Sprawling Agents.\n"), 0o600)
}

// ErrUnmanaged means a dir is named like an account but sprawl did not create it.
var ErrUnmanaged = errors.New("not a sprawl account")

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

// Adopt makes an existing dir named like an account an account (writes Marker).
func (s Store) Adopt(input string) (Account, error) {
	a, err := s.Define(input)
	if err != nil {
		return Account{}, err
	}
	if !isDir(a.Dir) {
		return Account{}, NotFoundError{a.Ref}
	}
	return a, Mark(a)
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
		if isDir(m) && ValidateName(name) == nil && name != Main {
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
		return "", fmt.Errorf("%w %q: refusing to remove a provider's default account", ErrReserved, a.Label())
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

// TrashDir is ~/.Trash on macOS and the freedesktop trash's files dir elsewhere.
func TrashDir(home string) string {
	files, _ := trashDirs(home)
	return files
}

// trashDirs returns where trashed files go and, for the freedesktop trash, where their
// .trashinfo files go ("" on macOS).
func trashDirs(home string) (files, info string) {
	if mac := filepath.Join(home, ".Trash"); isDir(mac) {
		return mac, ""
	}
	base := config.DirsFor(home, "Trash").Data
	return filepath.Join(base, "files"), filepath.Join(base, "info")
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
