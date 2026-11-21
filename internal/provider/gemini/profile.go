package gemini

import (
	"fmt"

	"github.com/squadrn/squadrn/internal/provider"
)

// Profiles are settings.json keys checked against Gemini CLI 0.70.0's configuration reference
// (ADR 0013 §7).
type Profiles struct{}

func (Profiles) Profile(name string, options []string, ctx provider.ProfileContext) (map[string]any, error) {
	doc := map[string]any{}
	switch name {
	case provider.ProfileMinimal, provider.ProfileStandard, provider.ProfileStrict:
	default:
		return nil, fmt.Errorf("unknown profile %q", name)
	}
	if ctx.Notify {
		// Squadrn notifies instead; Gemini's own notifications would come twice.
		doc["general"] = map[string]any{"enableNotifications": false}
	}
	for _, o := range options {
		switch o {
		case "privacy", "no-attribution":
		default:
			return nil, fmt.Errorf("unknown option %q", o)
		}
	}
	return doc, nil
}
