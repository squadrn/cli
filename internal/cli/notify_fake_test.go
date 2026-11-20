package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/fsx"
)

// fakeNotifications replaces the desktop notifier for the whole test binary: a notification is
// appended as JSON to $SQUADRN_TEST_NOTIFY (dropped when unset) and delivered inline instead of
// by a detached process. No test reaches a real notifier.
func fakeNotifications() {
	sendDesktop = func(_ context.Context, d delivery, replaceID string) (string, string, error) {
		if p := os.Getenv("SQUADRN_TEST_NOTIFY"); p != "" {
			b, _ := json.Marshal(map[string]any{"delivery": d, "replace": replaceID})
			if err := fsx.AppendLine(p, b, 0o600); err != nil {
				return "", "", err
			}
		}
		return "fake", "7", nil
	}
	startDelivery = func(d delivery) error {
		home, err := config.Home()
		if err != nil {
			return err
		}
		h := hookEnv{stdout: io.Discard, stderr: io.Discard, now: time.Now}
		_, err = h.send(notifyStore(home), d)
		return err
	}
}
