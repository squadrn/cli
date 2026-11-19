package provider

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRunner struct {
	Agent
	calls atomic.Int32
	delay time.Duration
	reply func(configDir string, args []string) ([]byte, error)
}

func (f *fakeRunner) Output(ctx context.Context, configDir string, args ...string) ([]byte, error) {
	f.calls.Add(1)
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return f.reply(configDir, args)
}

func (f *fakeRunner) Auth(ctx context.Context, configDir string) (AuthStatus, error) {
	out, err := f.Output(ctx, configDir, "auth", "status")
	if err != nil {
		return AuthStatus{}, err
	}
	var s AuthStatus
	return s, json.Unmarshal(out, &s)
}

func loggedInAs(configDir string, _ []string) ([]byte, error) {
	return []byte(`{"loggedIn":true,"email":"u@` + filepath.Base(configDir+"main") + `"}`), nil
}

func TestAuthAllRunsConcurrentlyAndKeepsOrder(t *testing.T) {
	r := &fakeRunner{delay: 100 * time.Millisecond, reply: loggedInAs}
	dirs := targets("", "/h/.claude-a", "/h/.claude-b", "/h/.claude-c")
	start := time.Now()
	res := authAll(context.Background(), r, dirs, time.Second)
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Errorf("took %v; lookups are not concurrent", elapsed)
	}
	for i, want := range []string{"u@main", "u@.claude-amain", "u@.claude-bmain", "u@.claude-cmain"} {
		if res[i].Err != nil || res[i].Status.Email != want {
			t.Errorf("result %d = %+v", i, res[i])
		}
	}
}

func TestAuthAllAppliesTimeout(t *testing.T) {
	r := &fakeRunner{delay: time.Second, reply: loggedInAs}
	res := authAll(context.Background(), r, targets("/slow"), 50*time.Millisecond)
	if !errors.Is(res[0].Err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", res[0].Err)
	}
}

func TestAuthCacheServesFreshEntriesAndRefreshesStale(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := AuthCache{Path: filepath.Join(t.TempDir(), "auth.json"), TTL: time.Minute, Now: func() time.Time { return now }}
	r := &fakeRunner{reply: loggedInAs}
	dirs := targets("", "/h/.claude-a")

	c.Statuses(context.Background(), r, dirs, time.Second)
	c.Statuses(context.Background(), r, dirs, time.Second)
	if n := r.calls.Load(); n != 2 {
		t.Fatalf("fresh cache should skip lookups: %d calls", n)
	}

	now = now.Add(2 * time.Minute)
	res := c.Statuses(context.Background(), r, dirs, time.Second)
	if n := r.calls.Load(); n != 4 || !res[1].Status.LoggedIn {
		t.Fatalf("stale cache should re-query: %d calls, %+v", n, res[1])
	}

	c.Forget("/h/.claude-amain")
	c.Statuses(context.Background(), r, dirs, time.Second)
	if n := r.calls.Load(); n != 5 {
		t.Fatalf("forgotten entry should be re-queried once: %d calls", n)
	}
}

func TestAuthCacheKeepsFailuresBriefly(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := AuthCache{Path: filepath.Join(t.TempDir(), "auth.json"), TTL: time.Hour, ErrTTL: 15 * time.Second, Now: func() time.Time { return now }}
	r := &fakeRunner{reply: func(string, []string) ([]byte, error) { return nil, errors.New("down") }}
	c.Statuses(context.Background(), r, targets("/x"), time.Second)
	res := c.Statuses(context.Background(), r, targets("/x"), time.Second)
	if n := r.calls.Load(); n != 1 || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "down") {
		t.Fatalf("cached failure: %d calls, %+v", n, res[0])
	}
	now = now.Add(time.Minute)
	c.Statuses(context.Background(), r, targets("/x"), time.Second)
	if n := r.calls.Load(); n != 2 {
		t.Fatalf("expired failure should be re-queried: %d calls", n)
	}
	c.ErrTTL = 0
	c.Refresh(context.Background(), r, targets("/x"), time.Second)
	c.Statuses(context.Background(), r, targets("/x"), time.Second)
	if n := r.calls.Load(); n != 4 {
		t.Fatalf("without ErrTTL failures are not cached: %d calls", n)
	}
}

func TestAuthAllLimitsConcurrency(t *testing.T) {
	var cur, peak atomic.Int32
	r := &fakeRunner{reply: func(string, []string) ([]byte, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		cur.Add(-1)
		return []byte(`{"loggedIn":true}`), nil
	}}
	authAll(context.Background(), r, targets("1", "2", "3", "4", "5", "6", "7", "8", "9"), time.Second)
	if p := peak.Load(); p > maxConcurrentAuth {
		t.Fatalf("peak concurrency %d > %d", p, maxConcurrentAuth)
	}
}

// targets keys each config dir by itself ("" → "main").
func targets(dirs ...string) []Target {
	out := make([]Target, len(dirs))
	for i, d := range dirs {
		out[i] = Target{Key: d + "main", ConfigDir: d}
	}
	return out
}

func TestAuthCacheCached(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := AuthCache{Path: filepath.Join(t.TempDir(), "auth.json"), TTL: time.Minute, Now: func() time.Time { return now }}
	if _, ok := c.Cached("/amain"); ok {
		t.Fatal("empty cache hit")
	}
	c.Statuses(context.Background(), &fakeRunner{reply: loggedInAs}, targets("/a"), time.Second)
	if s, ok := c.Cached("/amain"); !ok || !s.LoggedIn {
		t.Fatalf("Cached = %+v, %v", s, ok)
	}
	c.Forget("missing")
	now = now.Add(time.Hour)
	if _, ok := c.Cached("/amain"); ok {
		t.Fatal("stale entry served")
	}
}
