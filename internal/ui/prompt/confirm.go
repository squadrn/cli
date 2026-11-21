package prompt

import "strings"

// confirmModel is a yes/no question: ← → (or Tab) switch, y and n answer at once.
type confirmModel struct {
	q   Confirm
	yes bool
}

func newConfirm(q Confirm) *confirmModel { return &confirmModel{q: q, yes: q.Default} }

func (m *confirmModel) setHeight(int) {}

func (m *confirmModel) update(k key) outcome {
	switch k.kind {
	case keyCancel:
		return cancelled
	case keyEsc:
		return back
	case keyEnter:
		return done
	case keyLeft, keyRight, keyUp, keyDown:
		m.yes = !m.yes
	case keyRune:
		switch k.r {
		case 'y', 'Y':
			m.yes = true
			return done
		case 'n', 'N':
			m.yes = false
			return done
		case 'h', 'l', 'j', 'k':
			m.yes = !m.yes
		}
	}
	return running
}

func (m *confirmModel) question() string {
	if m.q.Question != "" {
		return m.q.Question
	}
	return m.q.Title
}

func (m *confirmModel) choices(s style) string {
	yes, no := s.g.Off+" Yes", s.g.Off+" No"
	if m.yes {
		yes = s.paint(cCyan, s.g.On+" Yes")
	} else {
		no = s.paint(cCyan, s.g.On+" No")
	}
	return yes + "  " + no
}

func (m *confirmModel) view(s style) string {
	var b strings.Builder
	if m.q.Question == "" && len(m.q.Body) == 0 {
		b.WriteString(s.line(s.paint(cCyan, s.g.Active), m.q.Title+"  "+m.choices(s)))
	} else {
		b.WriteString(s.line(s.paint(cCyan, s.g.Active), m.q.Title))
		for _, l := range m.q.Body {
			b.WriteString(s.bar(l))
		}
		if len(m.q.Body) > 0 {
			b.WriteString(s.line(s.paint(cDim, s.g.Bar), ""))
		}
		b.WriteString(s.bar(m.question() + "  " + m.choices(s)))
	}
	d := " " + s.g.Dot + " "
	b.WriteString(s.hint("y/n answer" + d + s.g.LeftRight + " switch" + d + "enter confirm" + d + "esc back"))
	return b.String()
}

func (m *confirmModel) summary(s style) string {
	answer := "No"
	if m.yes {
		answer = "Yes"
	}
	var b strings.Builder
	if m.q.Question == "" {
		b.WriteString(s.collapsed(m.q.Title, answer))
	} else {
		b.WriteString(s.line(s.paint(cGreen, s.g.Done), m.q.Title))
	}
	for _, l := range m.q.Body {
		b.WriteString(s.bar(l))
	}
	if m.q.Question != "" {
		if len(m.q.Body) > 0 {
			b.WriteString(s.line(s.paint(cDim, s.g.Bar), ""))
		}
		b.WriteString(s.bar(m.q.Question + " " + s.paint(cDim, answer)))
	}
	b.WriteString(s.line(s.paint(cDim, s.g.Bar), ""))
	return b.String()
}
