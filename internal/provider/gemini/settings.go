package gemini

import (
	"bytes"
	"encoding/json"

	"github.com/squadrn/squadrn-cli/internal/provider"
)

// Settings is Gemini CLI's settings.json, which may hold // and /* */ comments: Gemini reads it
// through strip-json-comments (no trailing commas). Decode drops the comments; Encode writes
// plain JSON, so an account's own copy has none, and main's file is never rewritten while it has
// some (HasComments).
type Settings struct{}

func (Settings) Decode(b []byte) (map[string]any, error) {
	b, _ = stripComments(b)
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

// Deep: Gemini's settings categories, merged entry by entry.
func (Settings) Deep() []string {
	return []string{"general", "ui", "tools", "privacy", "context", "security", "hooks", "hooksConfig", "experimental"}
}

// Private are never taken from main: MCP servers may carry tokens (shared_mcp copies the
// allowlisted ones).
func (Settings) Private() []string { return []string{"mcpServers"} }

// FromMain drops squadrn's own hook entries from main's hooks: each account gets its own.
func (Settings) FromMain(doc map[string]any) { provider.DropSquadrnHooks(doc) }

// HasComments reports whether b holds a comment outside strings.
func (Settings) HasComments(b []byte) bool {
	_, found := stripComments(b)
	return found
}

// stripComments replaces // line and /* block */ comments outside JSON strings with spaces,
// keeping line breaks (and so the line numbers of errors), like strip-json-comments. An
// unterminated block comment runs to the end. found reports whether there was any.
func stripComments(b []byte) (out []byte, found bool) {
	out = make([]byte, len(b))
	copy(out, b)
	blank := func(from, to int) {
		for i := from; i < to; i++ {
			if out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
			}
		}
	}
	inString := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			switch c {
			case '\\':
				i++
			case '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			end := bytes.IndexByte(b[i:], '\n')
			if end < 0 {
				end = len(b) - i
			}
			blank(i, i+end)
			found = true
			i += end - 1
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			end := bytes.Index(b[i+2:], []byte("*/"))
			stop := len(b)
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			blank(i, stop)
			found = true
			i = stop - 1
		}
	}
	return out, found
}
