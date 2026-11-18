//go:build windows

package guard

// nativePath turns a Git Bash path (/c/Users/x) into a Windows one (C:/Users/x).
func nativePath(p string) string {
	if len(p) >= 2 && p[0] == '/' && (len(p) == 2 || p[2] == '/') {
		if c := p[1] | 0x20; c >= 'a' && c <= 'z' {
			return string(c-0x20) + ":" + p[2:] + map[bool]string{true: "/"}[len(p) == 2]
		}
	}
	return p
}
