package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/squadrn/cli/internal/fsx"
)

// The status line is configured in settings.json (documented):
// "statusLine": {"type": "command", "command": "...", "padding": 0, "refreshInterval": N}.
// Claude Code runs command through a shell (Git Bash, or PowerShell without it, on Windows) and
// passes the status JSON on stdin.

// StatusLineCommand returns the status line command configured in the settings file at path.
func StatusLineCommand(path string) (string, bool, error) {
	obj, err := readObject(path)
	if err != nil {
		return "", false, err
	}
	raw, ok := obj.get("statusLine")
	if !ok {
		return "", false, nil
	}
	var sl struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	// A status line that is not a command object is not one squadrn can wrap: treat it as none.
	_ = json.Unmarshal(raw, &sl)
	return sl.Command, sl.Command != "", nil
}

// SetStatusLineCommand points the status line at command, keeping its other settings (padding,
// refreshInterval) and every other key of the file in place. "" removes the status line. The
// write goes through links to the file they point to, so accounts sharing it all change.
func SetStatusLineCommand(path, command string) error {
	real := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		real = r
	}
	obj, err := readObject(real)
	if err != nil {
		return err
	}
	if command == "" {
		obj.del("statusLine")
	} else {
		sl := object{}
		if raw, ok := obj.get("statusLine"); ok {
			sl, _ = parseObject(raw)
		}
		if _, ok := sl.get("type"); !ok {
			sl.set("type", mustJSON("command"))
		}
		sl.set("command", mustJSON(command))
		obj.set("statusLine", sl.encode(""))
	}
	perm := fs.FileMode(0o600)
	if fi, err := os.Stat(real); err == nil {
		perm = fi.Mode().Perm()
	}
	return fsx.WriteBytes(real, append(obj.encode(""), '\n'), perm)
}

// object is a JSON object that keeps its keys in file order.
type object []member

type member struct {
	key string
	val json.RawMessage
}

func (o object) get(k string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == k {
			return m.val, true
		}
	}
	return nil, false
}

func (o *object) set(k string, v json.RawMessage) {
	for i := range *o {
		if (*o)[i].key == k {
			(*o)[i].val = v
			return
		}
	}
	*o = append(*o, member{k, v})
}

func (o *object) del(k string) {
	for i := range *o {
		if (*o)[i].key == k {
			*o = append((*o)[:i], (*o)[i+1:]...)
			return
		}
	}
}

func readObject(path string) (object, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && len(bytes.TrimSpace(b)) == 0) {
		return object{}, nil
	}
	if err != nil {
		return nil, err
	}
	o, err := parseObject(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return o, nil
}

func parseObject(b []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var o object
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o = append(o, member{k, v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b[dec.InputOffset():])) > 0 {
		return nil, errors.New("trailing data after the JSON object")
	}
	return o, nil
}

// encode writes the object with two-space indentation, as Claude Code does, at depth prefix.
func (o object) encode(prefix string) json.RawMessage {
	if len(o) == 0 {
		return json.RawMessage("{}")
	}
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, m := range o {
		var v bytes.Buffer
		if err := json.Indent(&v, m.val, prefix+"  ", "  "); err != nil {
			v.Reset()
			v.Write(m.val)
		}
		fmt.Fprintf(&b, "%s  %s: %s", prefix, mustJSON(m.key), v.Bytes())
		if i < len(o)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(prefix + "}")
	return b.Bytes()
}

func mustJSON(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}
