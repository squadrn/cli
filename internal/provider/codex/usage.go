package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/squadrn/squadrn/internal/usage"
)

// RateLimits reads the account's usage limits from the app server (account/rateLimits/read).
func (c CLI) RateLimits(ctx context.Context, configDir string) (usage.Snapshot, error) {
	r, err := c.appServer(ctx, configDir, request{Method: "account/rateLimits/read"})
	if err != nil {
		return usage.Snapshot{}, err
	}
	if r[0].Err != nil {
		return usage.Snapshot{}, fmt.Errorf("%w: %w", usage.ErrNoLimits, r[0].Err)
	}
	return parseRateLimits(r[0].Result, time.Now())
}

type rateWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"`
}

type rateSnapshot struct {
	Primary   *rateWindow `json:"primary"`
	Secondary *rateWindow `json:"secondary"`
}

// parseRateLimits maps the codex bucket (or the single-bucket view) onto squadrn's windows: the
// one of at most a day is the five-hour window, a longer one the seven-day window. Without a
// duration, primary is the short window and secondary the long one, as Codex reports them.
func parseRateLimits(raw json.RawMessage, now time.Time) (usage.Snapshot, error) {
	var r struct {
		RateLimits          rateSnapshot            `json:"rateLimits"`
		RateLimitsByLimitID map[string]rateSnapshot `json:"rateLimitsByLimitId"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return usage.Snapshot{}, fmt.Errorf("parse codex rate limits: %w", err)
	}
	snap := r.RateLimits
	if b, ok := r.RateLimitsByLimitID["codex"]; ok {
		snap = b
	}
	s := usage.Snapshot{ObservedAt: now}
	for i, w := range []*rateWindow{snap.Primary, snap.Secondary} {
		if w == nil {
			continue
		}
		win := &usage.Window{UsedPct: w.UsedPercent}
		if w.ResetsAt != nil {
			win.ResetsAt = time.Unix(*w.ResetsAt, 0)
		}
		long := i == 1
		if w.WindowDurationMins != nil {
			long = *w.WindowDurationMins > 24*60
		}
		if long {
			s.Seven = win
		} else {
			s.Five = win
		}
	}
	if s.Five == nil && s.Seven == nil {
		return usage.Snapshot{}, usage.ErrNoLimits
	}
	return s, nil
}
