package prompt

import (
	"strings"
	"unicode/utf8"
)

// style is how a view is drawn: glyphs, colors, and the screen width (0: unknown, no cutting).
type style struct {
	g     Glyphs
	color bool
	width int
}

// leaderWidth is how wide a collapsed question with its dotted leader is drawn at most.
const leaderWidth = 64

const (
	cCyan   = "36"
	cDim    = "2"
	cRed    = "31"
	cYellow = "33"
	cGreen  = "32"
	cBold   = "1"
)

func (s style) paint(code, text string) string {
	if !s.color || text == "" {
		return text
	}
	return "\033[" + code + "m" + text + "\033[0m"
}

// line is one frame line: the glyph, two spaces and the text, cut to the screen width.
func (s style) line(glyph, text string) string {
	if text == "" {
		return s.cut(glyph) + "\n"
	}
	return s.cut(glyph+"  "+text) + "\n"
}

// bar is a line inside the current step: │  text.
func (s style) bar(text string) string { return s.line(s.paint(cDim, s.g.Bar), text) }

// collapsed is an answered question: ◇  title ······· answer.
func (s style) collapsed(title, answer string) string {
	width := leaderWidth
	if s.width > 0 {
		width = min(width, s.width)
	}
	n := width - 3 - visibleLen(title) - visibleLen(answer) - 2
	if answer == "" || n < 2 {
		if s.width > 0 && answer != "" {
			// Too long for the screen: the title gives way, the answer stays visible.
			short := style{g: s.g, width: max(s.width-3-visibleLen(answer)-1, 1)}
			title = short.cut(title)
		}
		return s.line(s.paint(cGreen, s.g.Done), strings.TrimSpace(title+" "+s.paint(cDim, answer)))
	}
	return s.line(s.paint(cGreen, s.g.Done), title+" "+s.paint(cDim, strings.Repeat(s.g.Leader, n))+" "+s.paint(cDim, answer))
}

// hint is the dim key help under the active question.
func (s style) hint(text string) string { return s.line(s.paint(cCyan, s.g.End), s.paint(cDim, text)) }

// cut shortens text to the screen width, ending it with an ellipsis; ANSI sequences are kept and
// do not count.
func (s style) cut(text string) string {
	if s.width <= 0 || visibleLen(text) <= s.width {
		return text
	}
	limit := s.width - utf8.RuneCountInString(s.g.Ellipsis)
	var b strings.Builder
	n := 0
	colored := false
	for i := 0; i < len(text); {
		if text[i] == 0x1b {
			j := strings.IndexByte(text[i:], 'm')
			if j < 0 {
				break
			}
			b.WriteString(text[i : i+j+1])
			colored = text[i:i+j+1] != "\033[0m"
			i += j + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if n < limit {
			b.WriteRune(r)
		}
		n++
		i += size
	}
	b.WriteString(s.g.Ellipsis)
	if colored {
		b.WriteString("\033[0m")
	}
	return b.String()
}

// visibleLen counts the runes of text outside ANSI sequences.
func visibleLen(text string) int {
	n := 0
	for i := 0; i < len(text); {
		if text[i] == 0x1b {
			j := strings.IndexByte(text[i:], 'm')
			if j < 0 {
				return n
			}
			i += j + 1
			continue
		}
		_, size := utf8.DecodeRuneInString(text[i:])
		n++
		i += size
	}
	return n
}

// frame renders the Prompter's frame lines; Term, Lines and Script share it.
type frame struct{ style }

func (f frame) intro(title string) string {
	return f.line(f.g.Start, f.paint(cBold, title)) + f.line(f.paint(cDim, f.g.Bar), "")
}

func (f frame) note(title string, lines []string) string {
	var b strings.Builder
	b.WriteString(f.line(f.paint(cGreen, f.g.Done), title))
	for _, l := range lines {
		b.WriteString(f.bar(l))
	}
	b.WriteString(f.line(f.paint(cDim, f.g.Bar), ""))
	return b.String()
}

func (f frame) warn(msg string) string {
	return f.line(f.paint(cYellow, f.g.Warn), msg) + f.line(f.paint(cDim, f.g.Bar), "")
}

func (f frame) outro(msg string, more []string) string {
	var b strings.Builder
	b.WriteString(f.line(f.g.End, msg))
	for _, m := range more {
		b.WriteString(f.cut("   "+m) + "\n")
	}
	return b.String()
}

func (f frame) cancel(msg string) string { return f.line(f.paint(cRed, f.g.Cancel), msg) }
