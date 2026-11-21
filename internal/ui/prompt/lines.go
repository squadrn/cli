package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Lines asks with line prompts and no ANSI sequences, for a terminal that does not understand
// them: the same questions in the same order, answered by typing a line.
type Lines struct {
	in  *bufio.Reader
	out io.Writer
	f   frame
}

func NewLines(in io.Reader, out io.Writer, g Glyphs) *Lines {
	return &Lines{in: bufio.NewReader(in), out: out, f: frame{style{g: g}}}
}

func (l *Lines) write(s string) { _, _ = io.WriteString(l.out, s) }

func (l *Lines) Intro(title string)             { l.write(l.f.intro(title)) }
func (l *Lines) Note(title string, s ...string) { l.write(l.f.note(title, s)) }
func (l *Lines) Log(line string)                { l.write(l.f.bar(line)) }
func (l *Lines) Warn(msg string)                { l.write(l.f.warn(msg)) }
func (l *Lines) Outro(msg string, m ...string)  { l.write(l.f.outro(msg, m)) }
func (l *Lines) Cancel(msg string)              { l.write(l.f.cancel(msg)) }

// ask prints the prompt in the current step and reads one answer; the end of the input cancels.
func (l *Lines) ask(prompt string) (string, error) { return l.askAt(l.f.g.Bar, prompt) }

func (l *Lines) askAt(glyph, prompt string) (string, error) {
	l.write(glyph + "  " + prompt)
	line, err := l.in.ReadString('\n')
	if err != nil {
		if !errors.Is(err, io.EOF) {
			return "", err
		}
		if line == "" {
			l.write("\n")
			return "", ErrCancelled
		}
		l.write("\n")
	}
	return strings.TrimSpace(line), nil
}

func (l *Lines) say(msg string) { l.write(l.f.bar(msg)) }

func (l *Lines) listOptions(opts []Option) {
	cols := columnsOf(opts)
	for i, o := range opts {
		l.say(fmt.Sprintf("%d) %s", i+1, optionText(l.f.style, o, cols, false)))
	}
}

// find matches an answer with an option: its number, its value or the start of its label.
func find(opts []Option, answer string) (int, bool) {
	if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(opts) {
		return n - 1, true
	}
	a := strings.ToLower(answer)
	for i, o := range opts {
		if strings.ToLower(o.value()) == a {
			return i, true
		}
	}
	for i, o := range opts {
		if a != "" && strings.HasPrefix(strings.ToLower(o.Label), a) {
			return i, true
		}
	}
	return -1, false
}

func (l *Lines) Select(q Select) (int, error) {
	l.write(l.f.line(l.f.g.Active, q.Title))
	l.listOptions(q.Options)
	def := min(max(q.Default, 0), len(q.Options)-1)
	for {
		answer, err := l.ask(fmt.Sprintf("1-%d [%d]: ", len(q.Options), def+1))
		if err != nil {
			return -1, err
		}
		if answer == "" {
			answer = strconv.Itoa(def + 1)
		}
		i, ok := find(q.Options, answer)
		switch {
		case !ok:
			l.say(fmt.Sprintf("no choice %q", answer))
		case q.Options[i].Disabled:
			l.say(fmt.Sprintf("%s cannot be chosen: %s", q.Options[i].Label, q.Options[i].Status))
		default:
			l.write(l.f.line(l.f.g.Bar, ""))
			return i, nil
		}
	}
}

func (l *Lines) MultiSelect(q MultiSelect) ([]int, error) {
	l.write(l.f.line(l.f.g.Active, q.Title))
	l.listOptions(q.Options)
	def := make([]string, len(q.Checked))
	for i, c := range q.Checked {
		def[i] = strconv.Itoa(c + 1)
	}
	shown := strings.Join(def, ",")
	if shown == "" {
		shown = "none"
	}
next:
	for {
		answer, err := l.ask(fmt.Sprintf("numbers or names, comma separated, - for none [%s]: ", shown))
		if err != nil {
			return nil, err
		}
		if answer == "" {
			l.write(l.f.line(l.f.g.Bar, ""))
			return slices.Sorted(slices.Values(append([]int{}, q.Checked...))), nil
		}
		out := []int{}
		if answer != "-" {
			for _, part := range strings.Split(answer, ",") {
				i, ok := find(q.Options, strings.TrimSpace(part))
				if !ok || q.Options[i].Disabled {
					l.say(fmt.Sprintf("no choice %q", strings.TrimSpace(part)))
					continue next
				}
				if !slices.Contains(out, i) {
					out = append(out, i)
				}
			}
		}
		slices.Sort(out)
		l.write(l.f.line(l.f.g.Bar, ""))
		return out, nil
	}
}

func (l *Lines) Text(q Text) (string, error) {
	l.write(l.f.line(l.f.g.Active, q.Title))
	prompt := "> "
	switch {
	case q.Initial != "":
		prompt = "[" + q.Initial + "]: "
	case q.Placeholder != "":
		prompt = "(" + q.Placeholder + "): "
	}
	for {
		answer, err := l.ask(prompt)
		if err != nil {
			return "", err
		}
		if answer == "" {
			answer = q.Initial
		}
		if err := q.check(answer); err != nil {
			l.say(err.Error())
			continue
		}
		if q.Hint != nil {
			if h := q.Hint(answer); h.Text != "" {
				l.say(h.Text)
			}
		}
		l.write(l.f.line(l.f.g.Bar, ""))
		return answer, nil
	}
}

func (l *Lines) Confirm(q Confirm) (bool, error) {
	choices := "[y/N]"
	if q.Default {
		choices = "[Y/n]"
	}
	question, glyph := q.Title, l.f.g.Active
	if q.Question != "" || len(q.Body) > 0 {
		l.write(l.f.line(l.f.g.Active, q.Title))
		for _, b := range q.Body {
			l.say(b)
		}
		if q.Question != "" {
			question = q.Question
		}
		glyph = l.f.g.Bar
	}
	for {
		answer, err := l.askAt(glyph, question+" "+choices+" ")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "":
			l.write(l.f.line(l.f.g.Bar, ""))
			return q.Default, nil
		case "y", "yes":
			l.write(l.f.line(l.f.g.Bar, ""))
			return true, nil
		case "n", "no":
			l.write(l.f.line(l.f.g.Bar, ""))
			return false, nil
		}
		l.say("answer y or n")
	}
}
