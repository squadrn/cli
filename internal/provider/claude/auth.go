package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/squadrn/squadrn-cli/internal/provider"
)

// auth reads `claude auth status`. Logged-out accounts print valid JSON but exit non-zero,
// so stdout is trusted whenever it is present.
func auth(ctx context.Context, r provider.Runner, configDir string) (provider.AuthStatus, error) {
	out, err := r.Output(ctx, configDir, "auth", "status")
	if len(bytes.TrimSpace(out)) == 0 {
		if err == nil {
			err = errors.New("claude auth status printed nothing")
		}
		return provider.AuthStatus{}, err
	}
	var s provider.AuthStatus
	if jerr := json.Unmarshal(out, &s); jerr != nil {
		return provider.AuthStatus{}, fmt.Errorf("parse claude auth status: %w", jerr)
	}
	return s, nil
}
