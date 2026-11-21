package prompt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
)

// Term asks at a terminal that understands ANSI: each question is drawn in place in raw mode
// (always restored) and collapses to one line once answered.
type Term struct {
	in  io.Reader
	out io.Writer
	style
	// pending are keys read past the end of the last question (typed ahead).
	pending []key
}

// NewTerm asks on out (stderr), reading keys from in (stdin).
func NewTerm(in io.Reader, out io.Writer, color bool, g Glyphs) *Term {
	return &Term{in: in, out: out, style: style{g: g, color: color}}
}

func (t *Term) frame() frame {
	t.measure(nil)
	return frame{t.style}
}

func (t *Term) write(s string) { _, _ = io.WriteString(t.out, s) }

func (t *Term) Intro(title string)             { t.write(t.frame().intro(title)) }
func (t *Term) Note(title string, l ...string) { t.write(t.frame().note(title, l)) }
func (t *Term) Log(line string)                { t.write(t.frame().bar(line)) }
func (t *Term) Warn(msg string)                { t.write(t.frame().warn(msg)) }
func (t *Term) Outro(msg string, m ...string)  { t.write(t.frame().outro(msg, m)) }
func (t *Term) Cancel(msg string)              { t.write(t.frame().cancel(msg)) }

func (t *Term) Select(q Select) (int, error) {
	m := newSelect(q)
	if err := t.ask(m); err != nil {
		return -1, err
	}
	return m.chosen, nil
}

func (t *Term) MultiSelect(q MultiSelect) ([]int, error) {
	m := newMultiSelect(q)
	if err := t.ask(m); err != nil {
		return nil, err
	}
	return m.checkedList(), nil
}

func (t *Term) Text(q Text) (string, error) {
	m := newText(q)
	if err := t.ask(m); err != nil {
		return "", err
	}
	return m.value(), nil
}

func (t *Term) Confirm(q Confirm) (bool, error) {
	m := newConfirm(q)
	if err := t.ask(m); err != nil {
		return false, err
	}
	return m.yes, nil
}

// measure reads the screen size before a draw; m (when set) learns the height.
func (t *Term) measure(m model) {
	f, ok := t.out.(*os.File)
	if !ok {
		return
	}
	if w, h, err := term.GetSize(f.Fd()); err == nil {
		t.width = w
		if m != nil {
			m.setHeight(h)
		}
	}
}

// ask runs m until it is answered, cancelled or sent back; the answered question is left on the
// screen as its summary.
func (t *Term) ask(m model) error {
	if f, ok := t.in.(*os.File); ok && term.IsTerminal(f.Fd()) {
		state, err := term.MakeRaw(f.Fd())
		if err != nil {
			return fmt.Errorf("prompt: %w", err)
		}
		defer func() { _ = term.Restore(f.Fd(), state) }()
	}
	s := &screen{out: t.out}
	t.write("\033[?25l") // hide the cursor while the question is shown
	defer t.write("\033[?25h")
	finish := func(o outcome) error {
		s.erase()
		switch o {
		case back:
			return ErrBack
		case cancelled:
			return ErrCancelled
		}
		t.write(strings.ReplaceAll(m.summary(t.style), "\n", "\r\n"))
		return nil
	}
	buf := make([]byte, 256)
	for {
		for len(t.pending) > 0 {
			k := t.pending[0]
			t.pending = t.pending[1:]
			if o := m.update(k); o != running {
				return finish(o)
			}
		}
		t.measure(m)
		s.draw(m.view(t.style))
		n, err := t.in.Read(buf)
		t.pending = append(t.pending, decodeKeys(buf[:n])...)
		if err != nil && len(t.pending) == 0 {
			s.erase()
			if errors.Is(err, io.EOF) {
				return ErrCancelled
			}
			return fmt.Errorf("prompt: %w", err)
		}
	}
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
