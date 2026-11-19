package proc

// Alive reports whether pid runs and, when started (a StartTime value) is known, is still the
// same process: pids are reused.
func Alive(pid int, started string) bool {
	if pid <= 0 || !running(pid) {
		return false
	}
	if started == "" {
		return true
	}
	now := StartTime(pid)
	return now == "" || now == started
}
