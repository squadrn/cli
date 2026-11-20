package notify

import "strings"

// Terminal notification protocols.
const (
	osc9   = "osc9"   // iTerm2, WezTerm
	osc777 = "osc777" // Ghostty, Warp
	osc99  = "osc99"  // kitty
)

// Terminal names the terminal the session runs in and the notification sequence it shows
// ("" when it has none squadrn knows). env reads the session's environment.
func Terminal(env func(string) string) (name, protocol string) {
	switch tp := env("TERM_PROGRAM"); {
	case tp == "iTerm.app":
		return "iTerm2", osc9
	case tp == "WezTerm":
		return "WezTerm", osc9
	case tp == "ghostty":
		return "Ghostty", osc777
	case tp == "WarpTerminal":
		return "Warp", osc777
	case env("KITTY_WINDOW_ID") != "" || env("TERM") == "xterm-kitty":
		return "kitty", osc99
	case tp != "":
		return tp, ""
	}
	return "", ""
}

// Sequence is the escape sequence that shows title and body as a notification in a terminal
// speaking protocol, with a bell after it when sound is set; "" for an unknown protocol.
func Sequence(protocol, title, body string, sound bool) string {
	title, body = printable(title), printable(body)
	var s string
	switch protocol {
	case osc9:
		s = "\x1b]9;" + title + ": " + body + "\a"
	case osc777:
		s = "\x1b]777;notify;" + strings.ReplaceAll(title, ";", ",") + ";" + strings.ReplaceAll(body, ";", ",") + "\a"
	case osc99:
		s = "\x1b]99;i=squadrn:d=0;" + title + "\x1b\\" + "\x1b]99;i=squadrn:d=1:p=body;" + body + "\x1b\\"
	default:
		return ""
	}
	if sound {
		s += "\a"
	}
	return s
}

// printable drops control characters, so text cannot end the sequence or start another.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 {
			return -1
		}
		return r
	}, s)
}
