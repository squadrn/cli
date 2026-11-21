package prompt

import (
	"strings"
)

// multiModel is a list of options to check with Space.
type multiModel struct {
	*selectModel
	checked []bool
}

func newMultiSelect(q MultiSelect) *multiModel {
	m := &multiModel{selectModel: newSelect(Select{Title: q.Title, Options: q.Options}), checked: make([]bool, len(q.Options))}
	for _, i := range q.Checked {
		if i >= 0 && i < len(m.checked) {
			m.checked[i] = true
		}
	}
	return m
}

func (m *multiModel) update(k key) outcome {
	switch {
	case k.kind == keyRune && k.r == ' ':
		if vis := m.visible(); len(vis) > 0 && !m.opts[vis[m.cursor]].Disabled {
			m.checked[vis[m.cursor]] = !m.checked[vis[m.cursor]]
		}
		return running
	case k.kind == keyEnter:
		return done
	}
	return m.selectModel.update(k)
}

func (m *multiModel) checkedList() []int {
	out := []int{}
	for i, c := range m.checked {
		if c {
			out = append(out, i)
		}
	}
	return out
}

func (m *multiModel) view(s style) string {
	var b strings.Builder
	b.WriteString(s.line(s.paint(cCyan, s.g.Active), m.title))
	cols := columnsOf(m.opts)
	vis := m.visible()
	end := min(len(vis), m.offset+m.rows())
	for pos := m.offset; pos < end; pos++ {
		i := vis[pos]
		box := s.g.Unchecked
		if m.checked[i] {
			box = s.paint(cGreen, s.g.Checked)
		}
		if pos == m.cursor {
			box = s.paint(cCyan, box)
		}
		b.WriteString(s.bar(box + " " + optionText(s, m.opts[i], cols, pos == m.cursor)))
	}
	d := " " + s.g.Dot + " "
	b.WriteString(s.hint(s.g.UpDown + " move" + d + "space toggle" + d + "enter confirm" + d + "esc back"))
	return b.String()
}

func (m *multiModel) summary(s style) string {
	var names []string
	for _, i := range m.checkedList() {
		names = append(names, m.opts[i].answer())
	}
	answer := strings.Join(names, ", ")
	if answer == "" {
		answer = "none"
	}
	return s.collapsed(m.title, answer) + s.line(s.paint(cDim, s.g.Bar), "")
}
