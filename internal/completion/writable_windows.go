package completion

// writable: Windows has no completion dirs; PowerShell loads completion from its profile.
func writable(string) bool { return false }
