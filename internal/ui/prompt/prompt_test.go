package prompt

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the view snapshots in testdata")

// feed sends raw terminal input to m and returns the outcome of the last key.
func feed(m model, input ...string) outcome {
	o := running
	for _, in := range input {
		for _, k := range decodeKeys([]byte(in)) {
			if o = m.update(k); o != running {
				return o
			}
		}
	}
	return o
}

func providers() []Option {
	return []Option{
		{Label: "Claude Code", Value: "claude", Detail: "2.1.0", Status: "3 accounts", Answer: "Claude Code 2.1.0"},
		{Label: "Codex CLI", Value: "codex", Detail: "0.171.0", Status: "new", Answer: "Codex CLI 0.171.0"},
		{Label: "Gemini CLI", Value: "gemini", Status: "not installed · npm install -g @google/gemini-cli", Disabled: true},
	}
}

func nameField() Text {
	return Text{
		Title:       "Account name",
		Placeholder: "e.g. work",
		Validate: func(s string) error {
			if strings.ContainsAny(s, " /") {
				return errors.New("letters, digits and - only")
			}
			return nil
		},
		Hint: func(s string) Hint {
			if s == "work" {
				return Hint{Text: `"work" is also claude/work: start it with srn codex/work`, Warn: true}
			}
			if s == "" {
				return Hint{}
			}
			return Hint{Text: "→ ~/.codex-" + s + " · start with: srn " + s}
		},
	}
}

func review() Confirm {
	return Confirm{
		Title:    "Review",
		Body:     []string{"account   codex/work → ~/.codex-work (private)", "profile   standard"},
		Question: "Create codex/work?",
		Default:  true,
	}
}

func TestSelectKeys(t *testing.T) {
	m := newSelect(Select{Title: "Provider", Options: providers(), Default: 1})
	if o := feed(m, "j", enter); o != done || m.chosen != 0 {
		t.Fatalf("j from codex skips the disabled gemini and wraps to claude: %v %d", o, m.chosen)
	}
	m = newSelect(Select{Title: "Provider", Options: providers()})
	if o := feed(m, "k", "\x1b[B", enter); o != done || m.chosen != 0 {
		t.Fatalf("k then down: %v %d", o, m.chosen)
	}
	m = newSelect(Select{Title: "Provider", Options: providers(), Default: 2})
	if m.cursor != 0 {
		t.Fatalf("a disabled default moves to the next enabled option: %d", m.cursor)
	}
	m.cursor = 2
	if o := feed(m, enter); o != running {
		t.Fatal("enter on a disabled option must not choose it")
	}
	for in, want := range map[string]outcome{esc: back, "\x03": cancelled, "\x04": cancelled} {
		if o := feed(newSelect(Select{Title: "P", Options: providers()}), in); o != want {
			t.Errorf("%q: %v, want %v", in, o, want)
		}
	}
	if newSelect(Select{Title: "P", Options: providers()}).summary(plain) != "" {
		t.Error("no summary before an answer")
	}
	m = newSelect(Select{Title: "P", Options: providers()})
	if feed(m, "x", bksp, "\x15", "\x1b[F", "\x1b[H", "\x1b[6~", "\x1b[5~"); m.cursor != 0 || m.filter != "" {
		t.Fatalf("other keys: cursor %d filter %q", m.cursor, m.filter)
	}
}

func TestMultiSelectKeys(t *testing.T) {
	opts := []Option{{Label: "privacy"}, {Label: "no-attribution"}, {Label: "off", Disabled: true}}
	m := newMultiSelect(MultiSelect{Title: "Options", Options: opts, Checked: []int{1, 7}})
	if o := feed(m, " ", "j", " ", "j", enter); o != done {
		t.Fatalf("outcome %v", o)
	}
	if got := fmt.Sprint(m.checkedList()); got != "[0]" {
		t.Fatalf("checked %s", got)
	}
	if o := feed(newMultiSelect(MultiSelect{Title: "O", Options: opts}), esc); o != back {
		t.Fatalf("esc: %v", o)
	}
	if s := newMultiSelect(MultiSelect{Title: "O", Options: opts}).summary(plain); !strings.Contains(s, "none") {
		t.Fatalf("summary of none: %q", s)
	}
}

func TestTextKeys(t *testing.T) {
	m := newText(nameField())
	if o := feed(m, enter); o != running || m.message(plain) != "required" {
		t.Fatalf("enter on empty: %v %q", o, m.message(plain))
	}
	feed(m, "my work")
	if m.err == nil || !strings.Contains(m.view(plain), "letters, digits and - only") {
		t.Fatalf("invalid text shows its error per key:\n%s", m.view(plain))
	}
	if o := feed(m, enter); o != running {
		t.Fatal("enter on invalid text")
	}
	if o := feed(m, "\x15", "wor", "kk", bksp, enter); o != done || m.value() != "work" {
		t.Fatalf("ctrl-u, retype: %v %q", o, m.value())
	}
	if o := feed(newText(Text{Title: "x", Optional: true}), enter); o != done {
		t.Fatal("optional empty text")
	}
	for in, want := range map[string]outcome{esc: back, "\x03": cancelled} {
		if o := feed(newText(nameField()), in); o != want {
			t.Errorf("%q: %v", in, o)
		}
	}
	if m := newText(Text{Title: "x", Initial: "a"}); m.value() != "a" || m.message(plain) != "" {
		t.Fatal("initial text")
	}
}

func TestConfirmKeys(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want bool
		o    outcome
	}{
		{[]string{enter}, true, done},
		{[]string{"n"}, false, done},
		{[]string{"\x1b[C", enter}, false, done},
		{[]string{"l", "h", "Y"}, true, done},
		{[]string{"N"}, false, done},
		{[]string{esc}, true, back},
		{[]string{"\x03"}, true, cancelled},
		{[]string{"q"}, true, running},
	} {
		m := newConfirm(Confirm{Title: "Log in now?", Default: true})
		if o := feed(m, c.in...); o != c.o || m.yes != c.want {
			t.Errorf("%q: %v %v", c.in, o, m.yes)
		}
	}
}

// TestViews snapshots every component's view and summary in UTF-8 and ASCII, without colour.
func TestViews(t *testing.T) {
	var b strings.Builder
	for _, g := range []struct {
		name string
		g    Glyphs
	}{{"utf8", Unicode}, {"ascii", ASCII}} {
		s := style{g: g.g}
		f := frame{s}
		section := func(name, text string) { fmt.Fprintf(&b, "-- %s %s --\n%s", g.name, name, text) }
		section("intro", f.intro("New account"))
		sel := newSelect(Select{Title: "Provider", Options: providers(), Default: 1})
		section("select", sel.view(s))
		feed(sel, enter)
		section("select answered", sel.summary(s))
		ms := newMultiSelect(MultiSelect{Title: "Options", Options: []Option{{Label: "privacy", Detail: "telemetry off"}, {Label: "no-attribution"}}, Checked: []int{0}})
		section("multiselect", ms.view(s))
		section("multiselect answered", ms.summary(s))
		txt := newText(nameField())
		section("text empty", txt.view(s))
		feed(txt, "home")
		section("text", txt.view(s))
		feed(txt, "\x15", "work")
		section("text warning", txt.view(s))
		section("text answered", txt.summary(s))
		feed(txt, " x")
		section("text invalid", txt.view(s))
		rv := newConfirm(review())
		section("review", rv.view(s))
		section("review answered", rv.summary(s))
		ln := newConfirm(Confirm{Title: "Log in now?", Default: true})
		section("confirm", ln.view(s))
		feed(ln, "n")
		section("confirm answered", ln.summary(s))
		section("note", f.note("Created codex/work", []string{g.g.Check + " ~/.codex-work (private)"}))
		section("warn", f.warn("not logged in · srn account login codex/work"))
		section("outro", f.outro("Ready in 3.2s · start: srn codex/work", []string{"Same without questions: srn account add codex/work --login"}))
		section("cancel", f.cancel("Cancelled · nothing changed"))
	}
	golden(t, "views.txt", b.String())
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs (go test -update to rewrite):\n%s", path, got)
	}
}

func TestColorAndCut(t *testing.T) {
	s := style{g: Unicode, color: true, width: 20}
	got := s.line(s.paint(cCyan, s.g.Active), s.paint(cDim, "a long title that does not fit"))
	if visibleLen(got) != 21 || !strings.HasSuffix(got, "\033[0m…\n") {
		t.Fatalf("cut: %q (%d)", got, visibleLen(got))
	}
	if got := (style{g: ASCII, width: 10}).cut("abcdefghijklmnop"); got != "abcdefg..." {
		t.Fatalf("ascii cut: %q", got)
	}
	if got := (style{g: Unicode, width: 3}).cut("\033[2mabcdef"); got != "\033[2mab…\033[0m" {
		t.Fatalf("open colour is closed: %q", got)
	}
	if got := (style{g: Unicode, width: 5}).cut("ab\033[1"); got != "ab\033[1" {
		t.Fatalf("broken sequence: %q", got)
	}
	if visibleLen("a\033[1") != 1 {
		t.Fatal("visibleLen of a broken sequence")
	}
	narrow := style{g: Unicode, width: 12}
	if got := narrow.collapsed("Provider", "Codex CLI 0.171.0"); strings.Contains(got, "··") {
		t.Fatalf("no leader when it does not fit: %q", got)
	}
	if got := narrow.collapsed("A long question", "Yes"); got != "◇  A lo… Yes\n" {
		t.Fatalf("the title gives way to the answer: %q", got)
	}
	m := newConfirm(Confirm{Title: "Go?"})
	if v := m.view(style{g: Unicode, color: true}); !strings.Contains(v, "\033[36m● No\033[0m") {
		t.Fatalf("coloured choice: %q", v)
	}
}

func TestDetectGlyphs(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	if DetectGlyphs(env("TERM", "dumb", "LANG", "en_US.UTF-8")) != ASCII {
		t.Error("TERM=dumb")
	}
	if pickGlyphs(env("LANG", "en_US.UTF-8")) != Unicode || pickGlyphs(env("LC_ALL", "C.utf8", "LANG", "C")) != Unicode {
		t.Error("UTF-8 locale")
	}
	if pickGlyphs(env("LC_CTYPE", "C", "LANG", "en_US.UTF-8")) != ASCII {
		t.Error("LC_CTYPE=C wins over LANG")
	}
	if pickGlyphs(env()) != localeDefault {
		t.Error("no locale")
	}
	if consoleUTF8() && DetectGlyphs(env("LANG", "en_US.UTF-8")) != Unicode {
		t.Error("UTF-8 terminal")
	}
}

func TestTerm(t *testing.T) {
	var out bytes.Buffer
	in := &slowReader{chunks: []string{"j", enter + " " + enter, "wo", "rk" + enter, "\x1b[C", enter, "y"}}
	p := NewTerm(in, &out, false, Unicode)
	p.Intro("New account")
	if i, err := p.Select(Select{Title: "Provider", Options: providers()}); err != nil || i != 1 {
		t.Fatalf("select %d %v", i, err)
	}
	if got, err := p.MultiSelect(MultiSelect{Title: "Options", Options: []Option{{Label: "privacy"}}}); err != nil || fmt.Sprint(got) != "[0]" {
		t.Fatalf("multiselect %v %v", got, err)
	}
	if s, err := p.Text(nameField()); err != nil || s != "work" {
		t.Fatalf("text %q %v", s, err)
	}
	if ok, err := p.Confirm(review()); err != nil || ok {
		t.Fatalf("confirm %v %v", ok, err)
	}
	if ok, err := p.Confirm(Confirm{Title: "Log in now?"}); err != nil || !ok {
		t.Fatalf("y %v %v", ok, err)
	}
	p.Note("Created", "x")
	p.Log("line")
	p.Warn("careful")
	p.Outro("done", "more")
	p.Cancel("gone")
	s := out.String()
	for _, want := range []string{"┌  New account", "◇  Provider ", " Codex CLI 0.171.0\r\n", "◇  Options ", "privacy", "Create codex/work? No", "│  line\n", "▲  careful", "└  done\n   more\n", "■  gone"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%q", want, s)
		}
	}
	if _, err := p.Text(nameField()); !errors.Is(err, ErrCancelled) {
		t.Fatalf("EOF: %v", err)
	}
	if _, err := NewTerm(&slowReader{chunks: []string{esc}}, &out, false, Unicode).Confirm(review()); !errors.Is(err, ErrBack) {
		t.Fatalf("esc: %v", err)
	}
	if _, err := NewTerm(&slowReader{chunks: []string{"\x03"}}, &out, false, Unicode).MultiSelect(MultiSelect{Title: "x"}); !errors.Is(err, ErrCancelled) {
		t.Fatalf("ctrl-c: %v", err)
	}
	if _, err := NewTerm(errReader{}, &out, false, Unicode).Select(Select{Title: "x", Options: providers()}); err == nil || errors.Is(err, ErrCancelled) {
		t.Fatalf("read error: %v", err)
	}
}

func TestTermMeasuresAFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	p := NewTerm(&slowReader{chunks: []string{enter}}, f, false, ASCII)
	if ok, err := p.Confirm(Confirm{Title: "ok?", Default: true}); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
}

func TestLines(t *testing.T) {
	var out bytes.Buffer
	answers := strings.Join([]string{
		"gemini", "x", "", // select: disabled, unknown, then the default
		"2, privacy", // multiselect
		"my work", "work",
		"maybe", "",
		"n",
		"",  // multiselect default
		"-", // multiselect none
		"", "",
	}, "\n") + "\n"
	p := NewLines(strings.NewReader(answers), &out, Unicode)
	p.Intro("New account")
	if i, err := p.Select(Select{Title: "Provider", Options: providers(), Default: 1}); err != nil || i != 1 {
		t.Fatalf("select %d %v", i, err)
	}
	opts := []Option{{Label: "privacy"}, {Label: "no-attribution"}}
	if got, err := p.MultiSelect(MultiSelect{Title: "Options", Options: opts}); err != nil || fmt.Sprint(got) != "[0 1]" {
		t.Fatalf("multiselect %v %v", got, err)
	}
	if s, err := p.Text(nameField()); err != nil || s != "work" {
		t.Fatalf("text %q %v", s, err)
	}
	if ok, err := p.Confirm(review()); err != nil || !ok {
		t.Fatalf("review %v %v", ok, err)
	}
	if ok, err := p.Confirm(Confirm{Title: "Log in now?", Default: true}); err != nil || ok {
		t.Fatalf("login %v %v", ok, err)
	}
	if got, err := p.MultiSelect(MultiSelect{Title: "Options", Options: opts, Checked: []int{1}}); err != nil || fmt.Sprint(got) != "[1]" {
		t.Fatalf("multiselect default %v %v", got, err)
	}
	if got, err := p.MultiSelect(MultiSelect{Title: "Options", Options: opts, Checked: []int{1}}); err != nil || len(got) != 0 {
		t.Fatalf("multiselect none %v %v", got, err)
	}
	if s, err := p.Text(Text{Title: "Alias", Initial: "sx"}); err != nil || s != "sx" {
		t.Fatalf("initial %q %v", s, err)
	}
	if ok, err := p.Confirm(Confirm{Title: "Sure?"}); err != nil || ok {
		t.Fatalf("default no %v %v", ok, err)
	}
	p.Note("Created", "x")
	p.Log("line")
	p.Warn("careful")
	p.Outro("done")
	p.Cancel("gone")
	if _, err := p.Select(Select{Title: "again", Options: providers()}); !errors.Is(err, ErrCancelled) {
		t.Fatalf("EOF: %v", err)
	}
	golden(t, "lines.txt", out.String())
	if s, err := NewLines(strings.NewReader("y"), &out, ASCII).Confirm(Confirm{Title: "x"}); err != nil || !s {
		t.Fatalf("answer without a newline: %v %v", s, err)
	}
	if _, err := NewLines(errReader{}, &out, ASCII).Text(Text{Title: "x"}); err == nil || errors.Is(err, ErrCancelled) {
		t.Fatalf("read error: %v", err)
	}
	if _, err := NewLines(strings.NewReader("zz\n"), &out, ASCII).MultiSelect(MultiSelect{Title: "x", Options: opts}); !errors.Is(err, ErrCancelled) {
		t.Fatalf("unknown then EOF: %v", err)
	}
}

func TestScript(t *testing.T) {
	ascii := ASCII
	s := &Script{Glyphs: &ascii, Answers: []any{
		"gemini", "codex", // disabled, then codex
		[]string{"nope"},
		[]string{"privacy"},
		"my work", "work",
		true, false,
		ErrBack,
	}}
	s.Intro("New account")
	if i, err := s.Select(Select{Title: "Provider", Options: providers()}); err != nil || i != 1 {
		t.Fatalf("select %d %v", i, err)
	}
	if got, err := s.MultiSelect(MultiSelect{Title: "Options", Options: []Option{{Label: "privacy"}}}); err != nil || fmt.Sprint(got) != "[0]" {
		t.Fatalf("multi %v %v", got, err)
	}
	if v, err := s.Text(nameField()); err != nil || v != "work" {
		t.Fatalf("text %q %v", v, err)
	}
	if ok, err := s.Confirm(review()); err != nil || !ok {
		t.Fatalf("confirm %v %v", ok, err)
	}
	if ok, err := s.Confirm(Confirm{Title: "Log in now?"}); err != nil || ok {
		t.Fatalf("confirm %v %v", ok, err)
	}
	if _, err := s.Select(Select{Title: "Setup", Options: providers()}); !errors.Is(err, ErrBack) {
		t.Fatalf("error answer: %v", err)
	}
	if _, err := s.Text(nameField()); !errors.Is(err, ErrCancelled) {
		t.Fatalf("no answers left: %v", err)
	}
	s.Note("Created", "x")
	s.Log("line")
	s.Warn("careful")
	s.Outro("done")
	s.Cancel("gone")
	if got := strings.Join(s.Asked, ","); got != "Provider,Provider,Options,Options,Account name,Account name,Review,Log in now?,Setup,Account name" {
		t.Fatalf("asked %s", got)
	}
	golden(t, "script.txt", s.Out.String())
	wrong := &Script{Answers: []any{true, true, 1, 1}}
	if _, err := wrong.Select(Select{Title: "x"}); err == nil {
		t.Fatal("bool for a select")
	}
	if _, err := wrong.MultiSelect(MultiSelect{Title: "x"}); err == nil {
		t.Fatal("bool for a multiselect")
	}
	if _, err := wrong.Text(Text{Title: "x"}); err == nil {
		t.Fatal("int for a text")
	}
	if _, err := wrong.Confirm(Confirm{Title: "x"}); err == nil {
		t.Fatal("int for a confirm")
	}
	empty := &Script{}
	if _, err := empty.Select(Select{Title: "x"}); !errors.Is(err, ErrCancelled) {
		t.Fatal(err)
	}
	if _, err := empty.MultiSelect(MultiSelect{Title: "x"}); !errors.Is(err, ErrCancelled) {
		t.Fatal(err)
	}
	if _, err := empty.Confirm(Confirm{Title: "x"}); !errors.Is(err, ErrCancelled) {
		t.Fatal(err)
	}
}
