package prompt

import "unicode/utf8"

type keyKind int

const (
	keyNone keyKind = iota
	keyRune
	keyEnter
	keyEsc
	keyCancel
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyPgUp
	keyPgDown
	keyBackspace
	keyClear
)

type key struct {
	kind keyKind
	r    rune
}

// decodeKeys turns raw-mode terminal input into keys. In raw mode Enter is CR, so LF is ctrl-j.
// Escape sequences arrive in one read; a lone ESC is keyEsc, ctrl-c and ctrl-d are keyCancel.
func decodeKeys(b []byte) []key {
	var keys []key
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b && i+1 < len(b) && (b[i+1] == '[' || b[i+1] == 'O'):
			j := i + 2
			for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
				j++
			}
			if j >= len(b) {
				return keys // truncated sequence: drop it
			}
			if k := csiKey(string(b[i+2:j]), b[j]); k != keyNone {
				keys = append(keys, key{kind: k})
			}
			i = j + 1
			continue
		case c == 0x1b:
			keys = append(keys, key{kind: keyEsc})
		case c == 0x03, c == 0x04: // ctrl-c, ctrl-d
			keys = append(keys, key{kind: keyCancel})
		case c == '\r':
			keys = append(keys, key{kind: keyEnter})
		case c == '\n', c == 0x0e, c == '\t': // ctrl-j, ctrl-n, tab
			keys = append(keys, key{kind: keyDown})
		case c == 0x0b, c == 0x10: // ctrl-k, ctrl-p
			keys = append(keys, key{kind: keyUp})
		case c == 0x15: // ctrl-u
			keys = append(keys, key{kind: keyClear})
		case c == 0x7f, c == 0x08:
			keys = append(keys, key{kind: keyBackspace})
		case c < 0x20:
		default:
			r, size := utf8.DecodeRune(b[i:])
			if r != utf8.RuneError {
				keys = append(keys, key{kind: keyRune, r: r})
			}
			i += size
			continue
		}
		i++
	}
	return keys
}

func csiKey(params string, final byte) keyKind {
	switch final {
	case 'A':
		return keyUp
	case 'B':
		return keyDown
	case 'C':
		return keyRight
	case 'D':
		return keyLeft
	case 'H':
		return keyHome
	case 'F':
		return keyEnd
	case 'Z': // shift-tab
		return keyUp
	case '~':
		switch params {
		case "1", "7":
			return keyHome
		case "4", "8":
			return keyEnd
		case "5":
			return keyPgUp
		case "6":
			return keyPgDown
		}
	}
	return keyNone
}
