package config

import (
	"fmt"
	"slices"
	"strings"
)

// windowsDeviceNames cannot be file names on Windows, with or without an extension.
var windowsDeviceNames = []string{
	"con", "prn", "aux", "nul",
	"com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
	"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9",
}

// powerShellAliases are the built-in aliases of Windows PowerShell 5.1 and PowerShell 7. An alias
// wins over a program on PATH, so a command named like one would never run in PowerShell.
var powerShellAliases = []string{
	"ac", "asnp", "cat", "cd", "chdir", "clc", "clear", "clhy", "cli", "clp", "cls", "clv", "cnsn",
	"compare", "copy", "cp", "cpi", "cpp", "curl", "cvpa", "dbp", "del", "diff", "dir", "dnsn", "ebp",
	"echo", "epal", "epcsv", "epsn", "erase", "etsn", "exsn", "fc", "fhx", "fl", "foreach", "ft", "fw",
	"gal", "gbp", "gc", "gcb", "gci", "gcm", "gcs", "gdr", "ghy", "gi", "gin", "gjb", "gl", "gm", "gmo",
	"gp", "gps", "gpv", "group", "gsn", "gsnp", "gsv", "gtz", "gu", "gv", "gwmi", "h", "history", "icm",
	"iex", "ihy", "ii", "ipal", "ipcsv", "ipmo", "ipsn", "irm", "ise", "iwmi", "iwr", "kill", "lp", "ls",
	"man", "md", "measure", "mi", "mount", "move", "mp", "mv", "nal", "ndr", "ni", "nmo", "npssc", "nsn",
	"nv", "ogv", "oh", "popd", "ps", "pushd", "pwd", "r", "rbp", "rcjb", "rcsn", "rd", "rdr", "ren", "ri",
	"rjb", "rm", "rmdir", "rmo", "rni", "rnp", "rp", "rsn", "rsnp", "rujb", "rv", "rvpa", "rwmi", "sajb",
	"sal", "saps", "sasv", "sbp", "sc", "scb", "select", "set", "shcm", "si", "sl", "sleep", "sls",
	"sort", "sp", "spjb", "spps", "spsv", "start", "stz", "sujb", "sv", "swmi", "tee", "trcm", "type",
	"wget", "where", "wjb", "write",
}

// CheckPortable refuses a new command name that would not work on every platform: Windows device
// names and PowerShell's built-in aliases. Names are checked on every platform so a config
// stays usable when it moves to another machine. Names already in use are not re-checked.
func CheckPortable(name string) error {
	base, _, _ := strings.Cut(strings.ToLower(name), ".")
	switch {
	case slices.Contains(windowsDeviceNames, base):
		return fmt.Errorf("%w %q: it is a reserved device name on Windows", ErrInvalidCommand, name)
	case slices.Contains(powerShellAliases, strings.ToLower(name)):
		return fmt.Errorf("%w %q: it is a built-in PowerShell alias, which PowerShell runs instead", ErrInvalidCommand, name)
	}
	return nil
}
