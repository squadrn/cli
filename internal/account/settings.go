package account

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/provider"
)

// ManagedFile records, in an account's dir (Account.Dir, next to Marker, never the CLI's state dir,
// which the CLI owns), the settings keys squadrn wrote to its settings file (ADR 0011).
const ManagedFile = "." + config.Slug + "-managed.json"

// Sources of a settings key.
const (
	SourceMain    = "main"
	SourceProfile = "profile"
	SourceSquadrn = "squadrn"
	// SourceUser: the user changed the key in the account; squadrn no longer touches it.
	SourceUser = "user"
)

// Managed is the content of ManagedFile.
type Managed struct {
	Version int                   `json:"version"`
	Profile string                `json:"profile,omitempty"`
	Options []string              `json:"options,omitempty"`
	Keys    map[string]ManagedKey `json:"keys"`
}

// ManagedKey is one settings key: where its value came from and the SHA-256 of the value
// squadrn wrote (or, for SourceUser, last saw; "" when the user removed it).
type ManagedKey struct {
	Source string `json:"source"`
	SHA256 string `json:"sha256,omitempty"`
}

// Layer is one source of an account's settings; later layers win.
type Layer struct {
	Source string
	Doc    map[string]any
}

// SettingsReport is what SyncSettings changed.
type SettingsReport struct {
	// Created: the account had no settings file of its own.
	Created bool
	// Backup is where a link to main's settings was moved (the one-time migration).
	Backup string
	// Written and Removed are the keys squadrn set or dropped; Kept the keys the user changed
	// since squadrn wrote them, now left to the user.
	Written, Removed, Kept []string
}

// Changed reports whether the sync wrote anything.
func (r SettingsReport) Changed() bool {
	return r.Created || r.Backup != "" || len(r.Written) > 0 || len(r.Removed) > 0
}

// SettingsPath is the account's settings file.
func SettingsPath(a Account) string { return filepath.Join(a.StateDir(), a.Spec.Settings) }

// ReadManaged reads an account's record; ok is false when there is none.
func ReadManaged(dir string) (m Managed, ok bool, err error) {
	b, err := os.ReadFile(filepath.Join(dir, ManagedFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Managed{Version: 1, Keys: map[string]ManagedKey{}}, false, nil
	} else if err != nil {
		return Managed{}, false, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return Managed{}, false, fmt.Errorf("parse %s: %w", filepath.Join(dir, ManagedFile), err)
	}
	if m.Keys == nil {
		m.Keys = map[string]ManagedKey{}
	}
	return m, true, nil
}

func writeManaged(dir string, m Managed) error {
	m.Version = 1
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return fsx.WriteBytes(filepath.Join(dir, ManagedFile), append(b, '\n'), 0o600)
}

// readSettings decodes a settings file; a missing file is an empty document.
func readSettings(path string, f provider.SettingsFormat) (map[string]any, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, false, nil
	} else if err != nil {
		return nil, false, err
	}
	doc, err := f.Decode(b)
	if err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, true, nil
}

// SyncSettings brings account a's own settings file up to date: main's settings, then layers
// (profile, squadrn's keys), merged as ADR 0011 describes. Keys the user changed are kept and
// reported once. An account whose settings file is still a link to main's gets a copy (the link
// is moved aside). Main accounts are left alone.
func (s Store) SyncSettings(a Account, f provider.SettingsFormat, layers []Layer, now time.Time) (SettingsReport, error) {
	var r SettingsReport
	if a.IsMain() || a.Spec.Settings == "" || f == nil {
		return r, nil
	}
	mainPath := SettingsPath(s.account(a.Spec, Main))
	mainDoc, _, err := readSettings(mainPath, f)
	if err != nil {
		return r, err
	}
	if p, ok := f.(provider.PrivateSettings); ok {
		for _, k := range p.Private() {
			delete(mainDoc, k)
		}
	}
	if mf, ok := f.(provider.MainFilter); ok {
		mf.FromMain(mainDoc)
	}
	m, recorded, err := ReadManaged(a.Dir)
	if err != nil {
		return r, err
	}
	path := SettingsPath(a)
	if !recorded && linksTo(path, mainPath) {
		bak := fsx.BackupName(path, now)
		if _, err := os.Lstat(bak); err == nil {
			return r, fmt.Errorf("move %s aside: %s already exists", path, bak)
		}
		if err := os.Rename(path, bak); err != nil {
			return r, fmt.Errorf("move %s aside: %w", path, err)
		}
		r.Backup = bak
	}
	doc, exists, err := readSettings(path, f)
	if err != nil {
		return r, err
	}
	r.Created = !exists && r.Backup == ""
	// squadrn's own hook entries in the account (the guard and notification hooks it writes
	// there) are neither compared with main's nor recorded: they are kept as they are.
	var own map[string][]any
	if _, ok := f.(provider.MainFilter); ok {
		own = provider.SplitSquadrnHooks(doc)
	}
	want := merge(append([]Layer{{Source: SourceMain, Doc: mainDoc}}, layers...), f.Deep())
	deep := f.Deep()
	before := len(m.Keys)
	paths := slices.Collect(maps.Keys(want))
	for p := range m.Keys {
		if _, ok := want[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	recordChanged := false
	for _, p := range paths {
		cur, has := getPath(doc, p, deep)
		w, wanted := want[p]
		rec, isRec := m.Keys[p]
		switch {
		case isRec && (rec.Source == SourceUser || rec.Source == SourceSquadrn):
			// The user's, or a key squadrn sets itself (ClaimSetting).
		case isRec && (!has || hash(cur) != rec.SHA256):
			m.Keys[p] = ManagedKey{Source: SourceUser, SHA256: hashIf(cur, has)}
			r.Kept = append(r.Kept, p)
			recordChanged = true
		case !wanted:
			delPath(doc, p, deep)
			delete(m.Keys, p)
			r.Removed = append(r.Removed, p)
		case has && !isRec && hash(cur) != hash(w.value):
			m.Keys[p] = ManagedKey{Source: SourceUser, SHA256: hash(cur)}
			r.Kept = append(r.Kept, p)
			recordChanged = true
		default:
			if !has || hash(cur) != hash(w.value) {
				setPath(doc, p, w.value, deep)
				r.Written = append(r.Written, p)
			}
			if k := (ManagedKey{Source: w.source, SHA256: hash(w.value)}); m.Keys[p] != k {
				m.Keys[p] = k
				recordChanged = true
			}
		}
	}
	if len(r.Written) > 0 || len(r.Removed) > 0 || !exists {
		provider.AddHooks(doc, own)
		b, err := f.Encode(doc)
		if err != nil {
			return r, err
		}
		if err := fsx.WriteBytes(path, b, 0o600); err != nil {
			return r, err
		}
	}
	if recordChanged || !recorded || len(m.Keys) != before {
		if err := writeManaged(a.Dir, m); err != nil {
			return r, err
		}
	}
	return r, nil
}

// SetManagedProfile records the profile and options an account was created with.
func SetManagedProfile(a Account, profile string, options []string) error {
	m, _, err := ReadManaged(a.Dir)
	if err != nil {
		return err
	}
	m.Profile, m.Options = profile, options
	return writeManaged(a.Dir, m)
}

// ClaimSetting records that squadrn itself set path in a's settings file (the usage status
// line): sync then leaves it alone. Release hands it back to main's value.
func ClaimSetting(a Account, f provider.SettingsFormat, path string) error {
	return recordSetting(a, f, path, SourceSquadrn)
}

func ReleaseSetting(a Account, f provider.SettingsFormat, path string) error {
	return recordSetting(a, f, path, SourceMain)
}

func recordSetting(a Account, f provider.SettingsFormat, path, source string) error {
	if a.IsMain() || f == nil {
		return nil
	}
	doc, _, err := readSettings(SettingsPath(a), f)
	if err != nil {
		return err
	}
	m, _, err := ReadManaged(a.Dir)
	if err != nil {
		return err
	}
	cur, has := getPath(doc, path, f.Deep())
	m.Keys[path] = ManagedKey{Source: source, SHA256: hashIf(cur, has)}
	return writeManaged(a.Dir, m)
}

type wanted struct {
	value  any
	source string
}

// merge flattens layers into key paths: deep objects one level down ("permissions.deny"),
// where arrays are the union of the layers in order; everything else is replaced whole.
func merge(layers []Layer, deep []string) map[string]wanted {
	out := map[string]wanted{}
	for _, l := range layers {
		for k, v := range l.Doc {
			sub, isMap := v.(map[string]any)
			if !slices.Contains(deep, k) || !isMap {
				out[k] = wanted{v, l.Source}
				continue
			}
			for sk, sv := range sub {
				p := k + "." + sk
				if prev, ok := out[p]; ok {
					if a, ok := prev.value.([]any); ok {
						if b, ok := sv.([]any); ok {
							sv = union(a, b)
						}
					}
				}
				out[p] = wanted{sv, l.Source}
			}
		}
	}
	return out
}

func union(a, b []any) []any {
	out := slices.Clone(a)
	seen := map[string]bool{}
	for _, v := range a {
		seen[hash(v)] = true
	}
	for _, v := range b {
		if h := hash(v); !seen[h] {
			seen[h] = true
			out = append(out, v)
		}
	}
	return out
}

// splitPath turns "permissions.deny" into its object and key when permissions is deep.
func splitPath(p string, deep []string) (parent, key string) {
	if k, sk, ok := strings.Cut(p, "."); ok && slices.Contains(deep, k) {
		return k, sk
	}
	return "", p
}

func getPath(doc map[string]any, p string, deep []string) (any, bool) {
	parent, key := splitPath(p, deep)
	if parent == "" {
		v, ok := doc[key]
		return v, ok
	}
	sub, ok := doc[parent].(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := sub[key]
	return v, ok
}

func setPath(doc map[string]any, p string, v any, deep []string) {
	parent, key := splitPath(p, deep)
	if parent == "" {
		doc[key] = v
		return
	}
	sub, ok := doc[parent].(map[string]any)
	if !ok {
		sub = map[string]any{}
		doc[parent] = sub
	}
	sub[key] = v
}

func delPath(doc map[string]any, p string, deep []string) {
	parent, key := splitPath(p, deep)
	if parent == "" {
		delete(doc, key)
		return
	}
	if sub, ok := doc[parent].(map[string]any); ok {
		delete(sub, key)
		if len(sub) == 0 {
			delete(doc, parent)
		}
	}
}

// hash is the SHA-256 of a value's canonical JSON (sorted keys), whatever file format held it.
func hash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(fmt.Sprint(v))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashIf(v any, has bool) string {
	if !has {
		return ""
	}
	return hash(v)
}

// MissingFromList returns the values the list at key (e.g. permissions.deny) of the settings file
// at path does not hold yet.
func MissingFromList(path string, f provider.SettingsFormat, key string, values []string) ([]string, error) {
	doc, _, err := readSettings(path, f)
	if err != nil {
		return nil, err
	}
	have, _ := getPath(doc, key, f.Deep())
	list, _ := have.([]any)
	var missing []string
	for _, v := range values {
		if !slices.Contains(list, any(v)) {
			missing = append(missing, v)
		}
	}
	return missing, nil
}

// ErrCommented: a settings file holds comments that re-encoding it would drop. Squadrn never
// rewrites such a file (main's Gemini settings.json, ADR 0013); it says what to add by hand.
var ErrCommented = errors.New("the file has comments squadrn would drop; edit it by hand")

// CheckRewritable refuses (ErrCommented) to rewrite the settings file at path when it holds
// comments f would drop. A missing file is fine.
func CheckRewritable(path string, f provider.SettingsFormat) error {
	c, ok := f.(provider.CommentedSettings)
	if !ok {
		return nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if c.HasComments(b) {
		return fmt.Errorf("%s: %w", path, ErrCommented)
	}
	return nil
}

// AddToList appends the values the list at key of the settings file at path does not hold yet,
// keeping everything else; a missing file is created. Links are followed, so main's file is
// edited in place. A file with comments the format would drop is refused (ErrCommented).
func AddToList(path string, f provider.SettingsFormat, key string, values []string) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if err := CheckRewritable(path, f); err != nil {
		return err
	}
	doc, _, err := readSettings(path, f)
	if err != nil {
		return err
	}
	have, _ := getPath(doc, key, f.Deep())
	list, ok := have.([]any)
	if have != nil && !ok {
		return fmt.Errorf("%s in %s is not a list", key, path)
	}
	for _, v := range values {
		if !slices.Contains(list, any(v)) {
			list = append(list, v)
		}
	}
	setPath(doc, key, list, f.Deep())
	b, err := f.Encode(doc)
	if err != nil {
		return err
	}
	return fsx.WriteBytes(path, b, 0o600)
}
