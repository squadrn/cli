package prompt

import "strings"

// textModel is a one-line text field, validated on every key.
type textModel struct {
	q     Text
	runes []rune
	err   error
	// tried: Enter was pressed, so an empty required field shows its error.
	tried bool
}

func newText(q Text) *textModel {
	m := &textModel{q: q, runes: []rune(q.Initial)}
	m.validate()
	return m
}

func (m *textModel) value() string { return string(m.runes) }

func (m *textModel) setHeight(int) {}

func (m *textModel) validate() { m.err = m.q.check(m.value()) }

func (m *textModel) update(k key) outcome {
	switch k.kind {
	case keyCancel:
		return cancelled
	case keyEsc:
		return back
	case keyEnter:
		m.tried = true
		if m.err == nil {
			return done
		}
	case keyBackspace:
		if len(m.runes) > 0 {
			m.runes = m.runes[:len(m.runes)-1]
		}
	case keyClear:
		m.runes = nil
	case keyRune:
		m.runes = append(m.runes, k.r)
	}
	m.validate()
	return running
}

// message is the line under the field: the error, else the hint.
func (m *textModel) message(s style) string {
	if m.err != nil && (len(m.runes) > 0 || m.tried) {
		return s.paint(cRed, m.err.Error())
	}
	if m.q.Hint == nil {
		return ""
	}
	h := m.q.Hint(m.value())
	if h.Warn {
		return s.paint(cYellow, h.Text)
	}
	return s.paint(cDim, h.Text)
}

func (m *textModel) view(s style) string {
	var b strings.Builder
	b.WriteString(s.line(s.paint(cCyan, s.g.Active), m.q.Title))
	field := m.value() + s.paint(cCyan, s.g.Cursor)
	if len(m.runes) == 0 && m.q.Placeholder != "" {
		field = s.paint(cCyan, s.g.Cursor) + s.paint(cDim, m.q.Placeholder)
	}
	b.WriteString(s.bar(field))
	if msg := m.message(s); msg != "" {
		b.WriteString(s.bar(msg))
	}
	d := " " + s.g.Dot + " "
	b.WriteString(s.hint("enter confirm" + d + "ctrl-u clear" + d + "esc back"))
	return b.String()
}

func (m *textModel) summary(s style) string {
	out := s.collapsed(m.q.Title, m.value())
	if m.q.Hint != nil {
		if h := m.q.Hint(m.value()); h.Warn {
			out += s.bar(s.paint(cYellow, h.Text))
		}
	}
	return out + s.line(s.paint(cDim, s.g.Bar), "")
}
