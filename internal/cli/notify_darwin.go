package cli

// notifyCommand passes text as arguments, never as script source, so titles cannot inject code.
func notifyCommand(title, body string) []string {
	return []string{
		"osascript",
		"-e", "on run argv",
		"-e", `display notification (item 2 of argv) with title (item 1 of argv) sound name "Glass"`,
		"-e", "end run",
		title, body,
	}
}
