// Package prompt asks questions in a frame (ADR 0014): select, multiselect, text and confirm,
// plus the frame lines around them. Each component is a pure model (update/view); a Prompter
// runs it at a raw-mode terminal (Term), as line prompts without ANSI (Lines) or from a script
// (Script, for tests). No TUI framework: nothing queries the terminal unless a question is shown.
package prompt

import "errors"

var (
	// ErrBack is Esc: go back one question. The caller decides what the first question does.
	ErrBack = errors.New("back")
	// ErrCancelled is Ctrl-C, Ctrl-D or the end of the input.
	ErrCancelled = errors.New("cancelled")
)

// Prompter asks questions and draws the frame around them. The frame goes to one stream
// (stderr); answers never reach stdout.
type Prompter interface {
	// Intro opens the frame: ┌  title.
	Intro(title string)
	// Note is a finished step with its lines: ◇  title, then │  line for each.
	Note(title string, lines ...string)
	// Log adds one line to the current step: │  line.
	Log(line string)
	// Warn is a warning step: ▲  msg.
	Warn(msg string)
	// Outro closes the frame: └  msg, then the more lines indented under it.
	Outro(msg string, more ...string)
	// Cancel closes the frame after a cancel: ■  msg.
	Cancel(msg string)

	// Select returns the index of the chosen option.
	Select(q Select) (int, error)
	// MultiSelect returns the indexes of the checked options, in order.
	MultiSelect(q MultiSelect) ([]int, error)
	// Text returns the typed text, validated.
	Text(q Text) (string, error)
	// Confirm returns the answer to a yes/no question.
	Confirm(q Confirm) (bool, error)
}

// Option is one choice of a Select or MultiSelect.
type Option struct {
	Label string
	// Value identifies the option for scripts; "" means Label.
	Value string
	// Detail is shown dimmed after the label (a version, "3 accounts").
	Detail string
	// Status is shown dimmed after the detail; for a disabled option it says why.
	Status string
	// Disabled options are listed dimmed and cannot be chosen.
	Disabled bool
	// Active marks a usable entry in Pick's list (●); inactive ones show ○ and their Status.
	Active bool
	// Answer is what the collapsed question shows; "" means Label.
	Answer string
}

func (o Option) value() string {
	if o.Value != "" {
		return o.Value
	}
	return o.Label
}

func (o Option) answer() string {
	if o.Answer != "" {
		return o.Answer
	}
	return o.Label
}

type Select struct {
	Title   string
	Options []Option
	// Default is the index the cursor starts on.
	Default int
}

type MultiSelect struct {
	Title   string
	Options []Option
	// Checked are the indexes checked at the start.
	Checked []int
}

// Hint is the line under a text field: a note, or a warning (yellow, not blocking).
type Hint struct {
	Text string
	Warn bool
}

type Text struct {
	Title       string
	Placeholder string
	// Initial is the text the field starts with (an earlier answer when going back).
	Initial string
	// Validate runs on every key; an error is shown and Enter is refused. Empty text is
	// refused unless Optional.
	Validate func(string) error
	// Hint describes valid text under the field; nil shows nothing.
	Hint     func(string) Hint
	Optional bool
}

type Confirm struct {
	Title string
	// Body lines are shown under the title, before the question (a review).
	Body []string
	// Question follows the body; "" asks the title itself.
	Question string
	Default  bool
}

// errEmpty is shown when Enter is pressed on an empty required field.
var errEmpty = errors.New("required")

func (q Text) check(s string) error {
	if s == "" {
		if q.Optional {
			return nil
		}
		return errEmpty
	}
	if q.Validate != nil {
		return q.Validate(s)
	}
	return nil
}
