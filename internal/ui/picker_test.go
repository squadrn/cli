package ui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func items() []Item {
	return []Item{
		{Label: "claude/main", Detail: "a@cmp.com", Active: true},
		{Label: "claude/personal", Detail: "b@cmp.com", Active: true},
		{Label: "claude/comacpro-1", Status: "not logged in"},
		{Label: "+ add a new account"},
	}
}

// press feeds raw terminal input to the picker.
func press(m picker, input ...string) picker {
	for _, in := range input {
		for _, k := range decodeKeys([]byte(in)) {
			if m.update(k) {
				return m
			}
		}
	}
	return m
}

const (
	down  = "\x1b[B"
	up    = "\x1b[A"
	enter = "\r"
	esc   = "\x1b"
	bksp  = "\x7f"
)

func TestPickerNavigatesAndWraps(t *testing.T) {
	if m := press(newPicker("›", items()), down, enter); m.chosen != 1 {
		t.Fatalf("chosen = %d", m.chosen)
	}
	if m := press(newPicker("›", items()), up, enter); m.chosen != 3 {
		t.Fatalf("up from the top should wrap to the last item, chosen = %d", m.chosen)
	}
	if m := press(newPicker("›", items()), "\x0e\x0e\x10", enter); m.chosen != 1 {
		t.Fatalf("ctrl-n ctrl-n ctrl-p: chosen = %d", m.chosen)
	}
	if m := press(newPicker("›", items()), "\x1bOB\t\x1b[Z", enter); m.chosen != 1 {
		t.Fatalf("app-mode down, tab, shift-tab: chosen = %d", m.chosen)
	}
}

func TestPickerFiltersAndMapsBackToItemIndex(t *testing.T) {
	if m := press(newPicker("›", items()), "comac", enter); m.chosen != 2 {
		t.Fatalf("chosen = %d", m.chosen)
	}
	if m := press(newPicker("›", items()), "b@", enter); m.chosen != 1 {
		t.Fatalf("filter should match the detail column, chosen = %d", m.chosen)
	}
	if m := press(newPicker("›", []Item{{Label: "tài-khoản"}, {Label: "x"}}), "tài", enter); m.chosen != 0 {
		t.Fatalf("multi-byte filter: chosen = %d", m.chosen)
	}
}

func TestPickerBackspaceAndNoMatch(t *testing.T) {
	m := press(newPicker("›", items()), "zz")
	if len(m.visible()) != 0 || !strings.Contains(m.View(), "no match") {
		t.Fatal("expected no match")
	}
	m = press(m, enter)
	if m.chosen != -1 {
		t.Fatal("enter with no match must not choose")
	}
	m = press(m, bksp, "\x08", bksp, enter)
	if m.chosen != 0 {
		t.Fatalf("after clearing the filter, chosen = %d", m.chosen)
	}
}

func TestPickerCancels(t *testing.T) {
	for _, in := range []string{esc, "\x03"} {
		if m := press(newPicker("›", items()), down, in); m.chosen != -1 {
			t.Fatalf("%q: chosen = %d", in, m.chosen)
		}
	}
}

func TestPickerView(t *testing.T) {
	v := newPicker("›", items()).View()
	for _, want := range []string{"claude/main", "a@cmp.com", "not logged in", "+ add a new account"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "\033[") {
		t.Error("colors without color")
	}
	m := newPicker("›", items())
	m.color = true
	if !strings.Contains(m.View(), "\033[1;94m▌ ") {
		t.Errorf("colored cursor missing:\n%q", m.View())
	}
}

func TestPickerScrollsInSmallTerminal(t *testing.T) {
	many := make([]Item, 20)
	for i := range many {
		many[i] = Item{Label: fmt.Sprintf("claude/a%02d", i), Active: true}
	}
	m := newPicker("›", many)
	m.height = 8
	if strings.Contains(m.View(), "claude/a10") || !strings.Contains(m.View(), "15 more") {
		t.Fatalf("view does not fit:\n%s", m.View())
	}
	m = press(m, "\x1b[F")
	if !strings.Contains(m.View(), "claude/a19") || m.offset != 15 {
		t.Fatalf("end not scrolled into view (offset %d):\n%s", m.offset, m.View())
	}
	m = press(m, "\x1b[5~", "\x1b[H", "\n")
	if m.cursor != 1 || m.offset != 0 {
		t.Fatalf("cursor %d offset %d", m.cursor, m.offset)
	}
	m = press(m, "\x1b[6~", "\x0b")
	if m.cursor != 5 {
		t.Fatalf("cursor after pgdown/ctrl-k = %d", m.cursor)
	}
	m = press(m, "\x1b[4~", "\x1b[1~")
	if m.cursor != 0 {
		t.Fatalf("cursor after end/home (~ form) = %d", m.cursor)
	}
}

func TestDecodeKeys(t *testing.T) {
	got := decodeKeys([]byte("a\x1b[3~\x1b[\x00"))
	if len(got) != 1 || got[0].kind != keyRune {
		t.Fatalf("unknown and truncated sequences: %+v", got)
	}
	if got := decodeKeys([]byte{0xff, 0x01}); len(got) != 0 {
		t.Fatalf("invalid bytes: %+v", got)
	}
}

// slowReader returns its chunks one Read at a time, like a terminal.
type slowReader struct{ chunks []string }

func (r *slowReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks = r.chunks[1:]
	return n, nil
}

func TestPick(t *testing.T) {
	var out bytes.Buffer
	i, err := Pick("squadrn ›", items(), &slowReader{chunks: []string{"pers", down, up, enter}}, &out, false)
	if err != nil || i != 1 {
		t.Fatalf("Pick = %d, %v", i, err)
	}
	s := out.String()
	if !strings.Contains(s, "squadrn › pers") || !strings.Contains(s, "\033[3F") || !strings.HasSuffix(s, "\r\033[J\033[?25h") {
		t.Fatalf("screen output:\n%q", s)
	}
	if _, err := Pick("›", items(), &slowReader{chunks: []string{"x"}}, &out, false); !errors.Is(err, ErrCancelled) {
		t.Fatalf("EOF: %v", err)
	}
	if _, err := Pick("›", items(), &slowReader{chunks: []string{esc}}, &out, false); !errors.Is(err, ErrCancelled) {
		t.Fatalf("esc: %v", err)
	}
	if _, err := Pick("›", items(), errReader{}, &out, false); err == nil || errors.Is(err, ErrCancelled) {
		t.Fatalf("read error: %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("broken tty") }

func TestSpin(t *testing.T) {
	var b strings.Builder
	var mu sync.Mutex
	stop := Spin(writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return b.Write(p) }), "checking")
	time.Sleep(100 * time.Millisecond)
	stop()
	stop()
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(b.String(), "checking") || !strings.HasSuffix(b.String(), "\r\033[K") {
		t.Fatalf("spinner output %q", b.String())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
