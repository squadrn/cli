package prompt

import (
	"fmt"
	"strings"
)

// Script answers questions from a list, for tests. Each answer is consumed by the next question:
//
//	string        Select: an option's value or label; Text: the text typed
//	int           Select: the option's index
//	[]string      MultiSelect: the values (or labels) checked; nil checks none
//	bool          Confirm
//	error         returned as it is (ErrBack, ErrCancelled)
//
// An answer the question refuses (a disabled option, text that does not validate) is written to
// the transcript and the next answer is tried. When the answers run out, ErrCancelled is returned,
// as at the end of the input. Out is the transcript Term would leave on the screen, uncoloured.
type Script struct {
	Answers []any
	Glyphs  *Glyphs
	Out     strings.Builder
	// Asked lists the titles of the questions in the order they were asked.
	Asked []string
}

func (s *Script) style() style {
	g := Unicode
	if s.Glyphs != nil {
		g = *s.Glyphs
	}
	return style{g: g}
}

func (s *Script) frame() frame { return frame{s.style()} }

func (s *Script) Intro(title string)             { s.Out.WriteString(s.frame().intro(title)) }
func (s *Script) Note(title string, l ...string) { s.Out.WriteString(s.frame().note(title, l)) }
func (s *Script) Log(line string)                { s.Out.WriteString(s.frame().bar(line)) }
func (s *Script) Warn(msg string)                { s.Out.WriteString(s.frame().warn(msg)) }
func (s *Script) Outro(msg string, m ...string)  { s.Out.WriteString(s.frame().outro(msg, m)) }
func (s *Script) Cancel(msg string)              { s.Out.WriteString(s.frame().cancel(msg)) }

// next takes the next answer; err is set for an error answer or when there are none left.
func (s *Script) next(title string) (any, error) {
	s.Asked = append(s.Asked, title)
	if len(s.Answers) == 0 {
		return nil, ErrCancelled
	}
	a := s.Answers[0]
	s.Answers = s.Answers[1:]
	if err, ok := a.(error); ok {
		return nil, err
	}
	return a, nil
}

func (s *Script) refuse(msg string) { s.Out.WriteString(s.frame().bar("! " + msg)) }

func (s *Script) Select(q Select) (int, error) {
	for {
		a, err := s.next(q.Title)
		if err != nil {
			return -1, err
		}
		var i int
		switch v := a.(type) {
		case int:
			i = v
		case string:
			i = matchOption(q.Options, v)
		default:
			return -1, fmt.Errorf("script: %q is a select, the answer is %#v", q.Title, a)
		}
		if i < 0 || i >= len(q.Options) || q.Options[i].Disabled {
			s.refuse(fmt.Sprintf("%v cannot be chosen", a))
			continue
		}
		m := newSelect(q)
		m.chosen = i
		s.Out.WriteString(m.summary(s.style()))
		return i, nil
	}
}

func matchOption(opts []Option, v string) int {
	for i, o := range opts {
		if o.value() == v || o.Label == v {
			return i
		}
	}
	return -1
}

func (s *Script) MultiSelect(q MultiSelect) ([]int, error) {
	for {
		a, err := s.next(q.Title)
		if err != nil {
			return nil, err
		}
		vs, ok := a.([]string)
		if !ok {
			return nil, fmt.Errorf("script: %q is a multiselect, the answer is %#v", q.Title, a)
		}
		m := newMultiSelect(MultiSelect{Title: q.Title, Options: q.Options})
		valid := true
		for _, v := range vs {
			i := matchOption(q.Options, v)
			if i < 0 || q.Options[i].Disabled {
				s.refuse(fmt.Sprintf("%v cannot be chosen", v))
				valid = false
				break
			}
			m.checked[i] = true
		}
		if !valid {
			continue
		}
		s.Out.WriteString(m.summary(s.style()))
		return m.checkedList(), nil
	}
}

func (s *Script) Text(q Text) (string, error) {
	for {
		a, err := s.next(q.Title)
		if err != nil {
			return "", err
		}
		v, ok := a.(string)
		if !ok {
			return "", fmt.Errorf("script: %q is a text, the answer is %#v", q.Title, a)
		}
		if err := q.check(v); err != nil {
			s.refuse(err.Error())
			continue
		}
		m := newText(q)
		m.runes = []rune(v)
		s.Out.WriteString(m.summary(s.style()))
		return v, nil
	}
}

func (s *Script) Confirm(q Confirm) (bool, error) {
	a, err := s.next(q.Title)
	if err != nil {
		return false, err
	}
	v, ok := a.(bool)
	if !ok {
		return false, fmt.Errorf("script: %q is a confirm, the answer is %#v", q.Title, a)
	}
	m := newConfirm(q)
	m.yes = v
	s.Out.WriteString(m.summary(s.style()))
	return v, nil
}
