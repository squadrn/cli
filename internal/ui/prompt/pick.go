package prompt

import (
	"fmt"
	"io"
	"strings"
)

// Item is one entry of Pick's list.
type Item = Option

// pickModel is the account picker: a filtering Select drawn as a bare list under its title, with
// the typed filter on the title line. Esc cancels, like Ctrl-C.
type pickModel struct{ *selectModel }

// pickChrome is the title line and the help line around the list.
const pickChrome = 2

func newPicker(title string, items []Item) pickModel {
	m := &selectModel{title: title, opts: items, chosen: -1, filtering: true, chrome: pickChrome}
	return pickModel{m}
}

func (m pickModel) update(k key) outcome {
	if k.kind == keyEsc {
		k.kind = keyCancel
	}
	return m.selectModel.update(k)
}

func (m pickModel) view(s style) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", s.paint(cBold, m.title), m.filter)
	width := labelWidth(m.opts)
	vis := m.visible()
	end := min(len(vis), m.offset+m.rows())
	for pos := m.offset; pos < end; pos++ {
		it := m.opts[vis[pos]]
		pointer := "  "
		label := pad(it.Label, width)
		if pos == m.cursor {
			pointer = s.paint("1;94", "▌ ")
			label = s.paint("1;94", label)
		}
		dot, status := s.paint("92", "●"), ""
		if !it.Active {
			dot, status = s.paint(cDim, "○"), s.paint(cDim, " "+it.Status)
		}
		if it.Detail == "" && it.Status == "" && !it.Active {
			dot = " "
		}
		fmt.Fprintf(&b, "%s%s  %s %s%s\n", pointer, label, dot, s.paint(cDim, it.Detail), status)
	}
	if len(vis) == 0 {
		b.WriteString(s.paint(cDim, "  no match") + "\n")
	}
	help := "↑↓/^j^k move · type to filter · enter open · esc cancel"
	if hidden := len(vis) - (end - m.offset); hidden > 0 {
		help = fmt.Sprintf("%d more · ", hidden) + help
	}
	b.WriteString(s.paint(cDim, "  "+help) + "\n")
	return b.String()
}

// summary is empty: the picker leaves nothing behind.
func (m pickModel) summary(style) string { return "" }

// Pick shows an inline picker on out, reading keys from in, and returns the chosen item index.
// A terminal in is switched to raw mode for the duration and always restored.
func Pick(title string, items []Item, in io.Reader, out io.Writer, color bool) (int, error) {
	m := newPicker(title, items)
	t := &Term{in: in, out: out, style: style{g: Unicode, color: color}}
	if err := t.ask(m); err != nil {
		return -1, err
	}
	return m.chosen, nil
}
