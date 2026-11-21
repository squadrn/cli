package claude

import (
	"bytes"
	"encoding/json"

	"github.com/squadrn/squadrn-cli/internal/provider"
)

// Settings is Claude Code's settings.json.
type Settings struct{}

func (Settings) Decode(b []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // numbers are written back exactly as read
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (Settings) Encode(doc map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Deep: objects whose entries come from several sources (main, the profile, squadrn).
func (Settings) Deep() []string {
	return []string{"permissions", "sandbox", "env", "hooks", "attribution"}
}

// FromMain drops squadrn's own hook entries from main's hooks: an account's guard and
// notification hooks come from its own setup, never from main (ADR 0013).
func (Settings) FromMain(doc map[string]any) { provider.DropSquadrnHooks(doc) }
