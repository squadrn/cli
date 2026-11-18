// Package ui holds interactive terminal widgets. It talks to the terminal directly (raw mode and
// ANSI sequences): no TUI framework, so nothing queries the terminal unless a widget is shown.
package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"
)

var ErrCancelled = errors.New("cancelled")

type Item struct {
	Label  string
	Detail string
	// Active marks a usable entry (●); inactive entries show ○ and their Status.
	Active bool
	Status string
}

type picker struct {
	title  string
	items  []Item
	filter string
	cursor int
	chosen int
	color  bool
	// height is the terminal height; the list scrolls when it does not fit. 0 means unknown.
	height int
	offset int
}

// chrome is the title line and the help line around the list.
const chrome = 2

func newPicker(title string, items []Item) picker {
	return picker{title: title, items: items, chosen: -1}
}

// visible returns indices of items matching the filter (case-insensitive substring).
func (m picker) visible() []int {
	f := strings.ToLower(m.filter)
	var idx []int
	for i, it := range m.items {
		if f == "" || strings.Contains(strings.ToLower(it.Label+" "+it.Detail), f) {
			idx = append(idx, i)
		}
	}
	return idx
}

// update applies one key and reports whether the picker is finished (chosen or cancelled).
func (m *picker) update(k key) bool {
	vis := m.visible()
	switch k.kind {
	case keyCancel:
		m.chosen = -1
		return true
	case keyEnter:
		if len(vis) > 0 {
			m.chosen = vis[m.cursor]
			return true
		}
	case keyUp:
		if len(vis) > 0 {
			m.cursor = (m.cursor - 1 + len(vis)) % len(vis)
		}
	case keyDown:
		if len(vis) > 0 {
			m.cursor = (m.cursor + 1) % len(vis)
		}
	case keyHome:
		m.cursor = 0
	case keyEnd:
		m.cursor = max(len(vis)-1, 0)
	case keyPgUp:
		m.cursor = max(m.cursor-m.rows(), 0)
	case keyPgDown:
		m.cursor = min(m.cursor+m.rows(), max(len(vis)-1, 0))
	case keyBackspace:
		if m.filter != "" {
			r := []rune(m.filter)
			m.filter = string(r[:len(r)-1])
			m.cursor = 0
		}
	case keyRune:
		m.filter += string(k.r)
		m.cursor = 0
	}
	m.scroll()
	return false
}

// rows is how many items fit on screen.
func (m picker) rows() int {
	if m.height <= chrome {
		return 1 << 30
	}
	return m.height - chrome - 1
}

// scroll keeps the cursor inside the visible window.
func (m *picker) scroll() {
	rows := m.rows()
	switch {
	case m.cursor < m.offset:
		m.offset = m.cursor
	case m.cursor >= m.offset+rows:
		m.offset = m.cursor - rows + 1
	}
	if n := len(m.visible()); m.offset > max(n-rows, 0) {
		m.offset = max(n-rows, 0)
	}
}

func (m picker) paint(code, s string) string {
	if !m.color || s == "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (m picker) View() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", m.paint("1", m.title), m.filter)
	width := 0
	for _, it := range m.items {
		width = max(width, utf8.RuneCountInString(it.Label))
	}
	vis := m.visible()
	end := min(len(vis), m.offset+m.rows())
	for pos := m.offset; pos < end; pos++ {
		it := m.items[vis[pos]]
		pointer := "  "
		label := it.Label + strings.Repeat(" ", width-utf8.RuneCountInString(it.Label))
		if pos == m.cursor {
			pointer = m.paint("1;94", "▌ ")
			label = m.paint("1;94", label)
		}
		dot, status := m.paint("92", "●"), ""
		if !it.Active {
			dot, status = m.paint("2", "○"), m.paint("2", " "+it.Status)
		}
		if it.Detail == "" && it.Status == "" && !it.Active {
			dot = " "
		}
		fmt.Fprintf(&b, "%s%s  %s %s%s\n", pointer, label, dot, m.paint("2", it.Detail), status)
	}
	if len(vis) == 0 {
		b.WriteString(m.paint("2", "  no match") + "\n")
	}
	help := "↑↓/^j^k move · type to filter · enter open · esc cancel"
	if hidden := len(vis) - (end - m.offset); hidden > 0 {
		help = fmt.Sprintf("%d more · ", hidden) + help
	}
	b.WriteString(m.paint("2", "  "+help) + "\n")
	return b.String()
}

// screen redraws a block of lines in place.
type screen struct {
	out   io.Writer
	lines int
}

func (s *screen) draw(view string) {
	s.erase()
	_, _ = io.WriteString(s.out, strings.ReplaceAll(view, "\n", "\r\n"))
	s.lines = strings.Count(view, "\n")
}

// erase moves to the first line drawn and clears everything below it.
func (s *screen) erase() {
	if s.lines > 0 {
		_, _ = fmt.Fprintf(s.out, "\033[%dF", s.lines)
	}
	_, _ = io.WriteString(s.out, "\r\033[J")
	s.lines = 0
}

// Pick shows an inline picker on out, reading keys from in, and returns the chosen item index.
// A terminal in is switched to raw mode for the duration and always restored.
func Pick(title string, items []Item, in io.Reader, out io.Writer, color bool) (int, error) {
	m := newPicker(title, items)
	m.color = color
	if f, ok := in.(*os.File); ok && term.IsTerminal(f.Fd()) {
		state, err := term.MakeRaw(f.Fd())
		if err != nil {
			return -1, fmt.Errorf("picker: %w", err)
		}
		defer func() { _ = term.Restore(f.Fd(), state) }()
	}
	outFile, _ := out.(*os.File)
	measure := func() {
		if outFile != nil {
			if _, h, err := term.GetSize(outFile.Fd()); err == nil {
				m.height = h
				m.scroll()
			}
		}
	}
	measure()
	s := &screen{out: out}
	_, _ = io.WriteString(out, "\033[?25l") // hide the cursor while the list is shown
	defer func() { _, _ = io.WriteString(out, "\033[?25h") }()
	s.draw(m.View())
	buf := make([]byte, 256)
	for {
		n, err := in.Read(buf)
		for _, k := range decodeKeys(buf[:n]) {
			if m.update(k) {
				s.erase()
				if m.chosen < 0 {
					return -1, ErrCancelled
				}
				return m.chosen, nil
			}
		}
		if err != nil {
			s.erase()
			if errors.Is(err, io.EOF) {
				return -1, ErrCancelled
			}
			return -1, fmt.Errorf("picker: %w", err)
		}
		measure()
		s.draw(m.View())
	}
}
