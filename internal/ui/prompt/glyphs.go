package prompt

import "strings"

// Glyphs are the frame and marker characters: Unicode, or ASCII where the terminal cannot show it.
type Glyphs struct {
	Start, Bar, Active, Done, Warn, Cancel, End                          string
	On, Off, Checked, Unchecked                                          string
	Check, Arrow, Dot, Leader, Ellipsis, Cursor, UpDown, LeftRight, Open string
}

var (
	Unicode = Glyphs{
		Start: "┌", Bar: "│", Active: "◆", Done: "◇", Warn: "▲", Cancel: "■", End: "└",
		On: "●", Off: "○", Checked: "◼", Unchecked: "◻",
		Check: "✓", Arrow: "→", Dot: "·", Leader: "·", Ellipsis: "…", Cursor: "▌", UpDown: "↑↓", LeftRight: "←→", Open: "↗",
	}
	ASCII = Glyphs{
		Start: "+", Bar: "|", Active: ">", Done: "*", Warn: "!", Cancel: "x", End: "\\",
		On: "(*)", Off: "( )", Checked: "[x]", Unchecked: "[ ]",
		Check: "v", Arrow: "->", Dot: "-", Leader: ".", Ellipsis: "...", Cursor: "_", UpDown: "up/down", LeftRight: "left/right", Open: "^",
	}
)

// DetectGlyphs picks ASCII when the locale is not UTF-8, TERM is dumb, or the console cannot show
// UTF-8 (a Windows console whose output code page is not 65001). getenv is os.Getenv.
func DetectGlyphs(getenv func(string) string) Glyphs {
	if getenv("TERM") == "dumb" || !consoleUTF8() {
		return ASCII
	}
	return pickGlyphs(getenv)
}

func pickGlyphs(getenv func(string) string) Glyphs {
	// The first variable set decides, as setlocale does.
	for _, v := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if s := getenv(v); s != "" {
			s = strings.ToLower(s)
			if strings.Contains(s, "utf-8") || strings.Contains(s, "utf8") {
				return Unicode
			}
			return ASCII
		}
	}
	return localeDefault
}
