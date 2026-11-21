package prompt

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// outcome is what a key did to a model.
type outcome int

const (
	running outcome = iota
	done
	back
	cancelled
)

// model is one question: keys change it, view draws it while it is asked, summary once answered.
type model interface {
	update(k key) outcome
	view(s style) string
	summary(s style) string
	setHeight(h int)
}

// selectModel is a list with a cursor. With filtering on (Pick), typed text filters the list;
// otherwise j and k move.
type selectModel struct {
	title     string
	opts      []Option
	filtering bool
	filter    string
	cursor    int // position in visible()
	chosen    int
	// height is the terminal height; the list scrolls when it does not fit. 0 means unknown.
	height int
	offset int
	// chrome is how many lines the view draws besides the list.
	chrome int
}

func newSelect(q Select) *selectModel {
	m := &selectModel{title: q.Title, opts: q.Options, chosen: -1, chrome: 3}
	if q.Default >= 0 && q.Default < len(q.Options) {
		m.cursor = q.Default
	}
	if len(m.opts) > 0 && m.opts[m.cursor].Disabled {
		m.move(1)
	}
	return m
}

func (m *selectModel) setHeight(h int) {
	m.height = h
	m.scroll()
}

// visible returns indices of options matching the filter (case-insensitive substring).
func (m *selectModel) visible() []int {
	f := strings.ToLower(m.filter)
	var idx []int
	for i, o := range m.opts {
		if f == "" || strings.Contains(strings.ToLower(o.Label+" "+o.Detail), f) {
			idx = append(idx, i)
		}
	}
	return idx
}

// move steps the cursor by d, wrapping and skipping disabled options.
func (m *selectModel) move(d int) {
	vis := m.visible()
	for range vis {
		m.cursor = (m.cursor + d + len(vis)) % len(vis)
		if !m.opts[vis[m.cursor]].Disabled {
			return
		}
	}
}

func (m *selectModel) update(k key) outcome {
	vis := m.visible()
	switch k.kind {
	case keyCancel:
		m.chosen = -1
		return cancelled
	case keyEsc:
		m.chosen = -1
		return back
	case keyEnter:
		if len(vis) > 0 && !m.opts[vis[m.cursor]].Disabled {
			m.chosen = vis[m.cursor]
			return done
		}
	case keyUp:
		if len(vis) > 0 {
			m.move(-1)
		}
	case keyDown:
		if len(vis) > 0 {
			m.move(1)
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
	case keyClear:
		if m.filtering {
			m.filter, m.cursor = "", 0
		}
	case keyRune:
		switch {
		case m.filtering:
			m.filter += string(k.r)
			m.cursor = 0
		case k.r == 'j':
			m.move(1)
		case k.r == 'k':
			m.move(-1)
		}
	}
	m.scroll()
	return running
}

// rows is how many options fit on screen.
func (m *selectModel) rows() int {
	if m.height <= m.chrome {
		return 1 << 30
	}
	return m.height - m.chrome - 1
}

// scroll keeps the cursor inside the visible window.
func (m *selectModel) scroll() {
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

func labelWidth(opts []Option) int {
	w := 0
	for _, o := range opts {
		w = max(w, utf8.RuneCountInString(o.Label))
	}
	return w
}

// columns are the widths of the labels and details of a list, to align them.
type columns struct{ label, detail int }

func columnsOf(opts []Option) columns {
	c := columns{label: labelWidth(opts)}
	for _, o := range opts {
		c.detail = max(c.detail, utf8.RuneCountInString(o.Detail))
	}
	return c
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(w-utf8.RuneCountInString(s), 0))
}

// optionText is an option's label, detail and status as one list entry.
func optionText(s style, o Option, c columns, current bool) string {
	label := pad(o.Label, c.label)
	switch {
	case o.Disabled:
		label = s.paint(cDim, label)
	case current:
		label = s.paint(cCyan, label)
	}
	text := label
	if c.detail > 0 {
		text += "  " + s.paint(cDim, pad(o.Detail, c.detail))
	}
	if o.Status != "" {
		text += "  " + s.paint(cDim, o.Status)
	}
	return strings.TrimRight(text, " ")
}

func (m *selectModel) view(s style) string {
	var b strings.Builder
	b.WriteString(s.line(s.paint(cCyan, s.g.Active), m.title))
	cols := columnsOf(m.opts)
	vis := m.visible()
	end := min(len(vis), m.offset+m.rows())
	for pos := m.offset; pos < end; pos++ {
		o := m.opts[vis[pos]]
		dot := s.g.Off
		if pos == m.cursor {
			dot = s.paint(cCyan, s.g.On)
		}
		if o.Disabled {
			dot = s.paint(cDim, s.g.Off)
		}
		b.WriteString(s.bar(dot + " " + optionText(s, o, cols, pos == m.cursor)))
	}
	help := s.g.UpDown + " move " + s.g.Dot + " enter select " + s.g.Dot + " esc back"
	if hidden := len(vis) - (end - m.offset); hidden > 0 {
		help = fmt.Sprintf("%d more · ", hidden) + help
	}
	b.WriteString(s.hint(help))
	return b.String()
}

func (m *selectModel) summary(s style) string {
	if m.chosen < 0 {
		return ""
	}
	return s.collapsed(m.title, m.opts[m.chosen].answer()) + s.line(s.paint(cDim, s.g.Bar), "")
}
