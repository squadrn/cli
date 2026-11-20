package fakeagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// notifiers are the desktop notifiers and probes squadrn runs for notifications (ADR 0012).
// Their fakes record each call as one JSON line in $HOME/notify.log, so no test shows a real
// notification or asks the window server anything.
var notifiers = map[string]bool{
	"terminal-notifier": true, "osascript": true, "lsappinfo": true,
	"notify-send": true, "gdbus": true, "powershell": true,
}

// NotifierNames lists the notifier fakes, sorted.
func NotifierNames() []string {
	var names []string
	for n := range notifiers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Notifier records one call. notify-send --print-id answers with an id; lsappinfo answers
// nothing, so the frontmost app stays unknown.
func Notifier(name string, args []string) int {
	call := map[string]any{"tool": name, "args": args}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "SQUADRN_TOAST_") {
			k, v, _ := strings.Cut(kv, "=")
			call[k] = v
		}
	}
	b, _ := json.Marshal(call)
	f, err := os.OpenFile(filepath.Join(os.Getenv("HOME"), "notify.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
	for _, a := range args {
		if name == "notify-send" && a == "--print-id" {
			_, _ = os.Stdout.WriteString("7\n")
		}
	}
	return 0
}
