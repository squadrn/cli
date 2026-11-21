package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/squadrn/squadrn-cli/internal/fsx"
)

// Target is one account to query: Key identifies it in the cache (the account label, since
// several providers' main accounts share the empty config dir), ConfigDir selects it.
type Target struct {
	Key       string
	ConfigDir string
}

type AuthResult struct {
	Status AuthStatus
	Err    error
}

// maxConcurrentAuth bounds parallel `claude auth status` processes (each is a node runtime).
const maxConcurrentAuth = 4

// authAll queries every target, at most maxConcurrentAuth at a time, each bounded by timeout.
// Results keep input order.
func authAll(ctx context.Context, ag Agent, targets []Target, timeout time.Duration) []AuthResult {
	results := make([]AuthResult, len(targets))
	sem := make(chan struct{}, maxConcurrentAuth)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			s, err := ag.Auth(cctx, t.ConfigDir)
			results[i] = AuthResult{Status: s, Err: err}
		})
	}
	wg.Wait()
	return results
}

// AuthCache memoises auth statuses on disk so the account picker opens instantly. Failures are
// cached briefly (ErrTTL) so a broken account does not stall every command. The file holds
// emails, so it is private to the user.
type AuthCache struct {
	Path   string
	TTL    time.Duration
	ErrTTL time.Duration
	Now    func() time.Time
}

type cacheEntry struct {
	Status AuthStatus `json:"status"`
	Err    string     `json:"err,omitempty"`
	At     time.Time  `json:"at"`
}

func (c AuthCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c AuthCache) read() map[string]cacheEntry {
	entries := map[string]cacheEntry{}
	if b, err := os.ReadFile(c.Path); err == nil {
		_ = json.Unmarshal(b, &entries)
	}
	return entries
}

func (c AuthCache) fresh(e cacheEntry) bool {
	ttl := c.TTL
	if e.Err != "" {
		ttl = c.ErrTTL
	}
	return c.now().Sub(e.At) < ttl
}

func (c AuthCache) store(entries map[string]cacheEntry, key string, res AuthResult) {
	e := cacheEntry{Status: res.Status, At: c.now()}
	if res.Err != nil {
		if c.ErrTTL <= 0 {
			delete(entries, key)
			return
		}
		e = cacheEntry{Err: res.Err.Error(), At: c.now()}
	}
	entries[key] = e
}

// Statuses returns cached results that are still fresh and queries the rest.
func (c AuthCache) Statuses(ctx context.Context, ag Agent, targets []Target, timeout time.Duration) []AuthResult {
	entries := c.read()
	results := make([]AuthResult, len(targets))
	var missing []Target
	var missingIdx []int
	for i, t := range targets {
		if e, ok := entries[t.Key]; ok && c.fresh(e) {
			results[i] = AuthResult{Status: e.Status}
			if e.Err != "" {
				results[i] = AuthResult{Err: errors.New(e.Err)}
			}
			continue
		}
		missing = append(missing, t)
		missingIdx = append(missingIdx, i)
	}
	if len(missing) == 0 {
		return results
	}
	for j, res := range authAll(ctx, ag, missing, timeout) {
		results[missingIdx[j]] = res
		c.store(entries, missing[j].Key, res)
	}
	c.write(entries)
	return results
}

// Refresh queries every target and replaces their cache entries.
func (c AuthCache) Refresh(ctx context.Context, ag Agent, targets []Target, timeout time.Duration) []AuthResult {
	results := authAll(ctx, ag, targets, timeout)
	entries := c.read()
	for i, res := range results {
		c.store(entries, targets[i].Key, res)
	}
	c.write(entries)
	return results
}

// Cached returns a fresh cached status without querying; ok is false when there is none.
func (c AuthCache) Cached(key string) (AuthStatus, bool) {
	e, ok := c.read()[key]
	if !ok || e.Err != "" || !c.fresh(e) {
		return AuthStatus{}, false
	}
	return e.Status, true
}

// Last returns the last status cached for key however old, without querying: for hints that
// must not wait (shell completion). ok is false when there is none or it was a failure.
func (c AuthCache) Last(key string) (AuthStatus, bool) {
	e, ok := c.read()[key]
	if !ok || e.Err != "" {
		return AuthStatus{}, false
	}
	return e.Status, true
}

// Forget drops one account, e.g. after login, logout or removal; false when none was cached.
func (c AuthCache) Forget(key string) bool {
	entries := c.read()
	if _, ok := entries[key]; !ok {
		return false
	}
	delete(entries, key)
	c.write(entries)
	return true
}

// write is best-effort: a cache failure must never fail the command.
func (c AuthCache) write(entries map[string]cacheEntry) {
	if b, err := json.Marshal(entries); err == nil {
		_ = fsx.WriteBytes(c.Path, b, 0o600)
	}
}
