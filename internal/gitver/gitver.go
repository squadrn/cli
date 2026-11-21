// Package gitver checks that the installed Git is new enough for squadrn and says how to upgrade.
package gitver

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/squadrn/squadrn-cli/internal/proc"
)

// Min is the oldest Git squadrn supports: worktree.Open relies on
// `git rev-parse --path-format=absolute` (Git 2.31).
var Min = Version{Major: 2, Minor: 31}

type Version struct {
	Major, Minor, Patch int
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// AtLeast reports whether v >= min.
func (v Version) AtLeast(min Version) bool {
	if v.Major != min.Major {
		return v.Major > min.Major
	}
	if v.Minor != min.Minor {
		return v.Minor > min.Minor
	}
	return v.Patch >= min.Patch
}

var versionRe = regexp.MustCompile(`^(?:git version )?(\d+)\.(\d+)(?:\.(\d+))?`)

// Parse reads `git version` output: "git version 2.39.5 (Apple Git-154)",
// "git version 2.47.1.windows.1", "git version 2.45.GIT", "2.43.0.rc1".
func Parse(out string) (Version, error) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return Version{}, fmt.Errorf("unrecognised git version output %q", strings.TrimSpace(out))
	}
	v := Version{}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		v.Patch, _ = strconv.Atoi(m[3])
	}
	return v, nil
}

// Detect runs `git version`.
func Detect(ctx context.Context) (Version, error) {
	out, err := proc.Output(ctx, proc.Options{}, "git", "version")
	if err != nil {
		return Version{}, fmt.Errorf("git is not installed or not working: %w", err)
	}
	return Parse(string(out))
}

// Require fails when the installed Git is older than Min, naming the command that upgrades it.
func Require(ctx context.Context) error {
	v, err := Detect(ctx)
	if err != nil {
		return err
	}
	if !v.AtLeast(Min) {
		return tooOld(v, Hint())
	}
	return nil
}

func tooOld(have Version, hint string) error {
	return fmt.Errorf("git %s is too old; squadrn needs git %d.%d or newer (upgrade: %s)", have, Min.Major, Min.Minor, hint)
}

// UpgradeHint is the command that installs a current Git on the given system. osRelease is the
// content of /etc/os-release on Linux ("" elsewhere).
func UpgradeHint(goos, osRelease string) string {
	switch goos {
	case "darwin":
		return "brew install git (or: xcode-select --install)"
	case "windows":
		return "winget install --id Git.Git -e"
	}
	f := parseOSRelease(osRelease)
	ids := " " + f["ID"] + " " + f["ID_LIKE"] + " "
	switch {
	case strings.Contains(ids, " ubuntu "):
		return "sudo add-apt-repository ppa:git-core/ppa && sudo apt update && sudo apt install git"
	case strings.Contains(ids, " debian "):
		if code := f["VERSION_CODENAME"]; code != "" {
			return fmt.Sprintf("sudo apt install -t %s-backports git", code)
		}
		return "install git from your distribution's backports"
	case strings.Contains(ids, " fedora "), strings.Contains(ids, " rhel "), strings.Contains(ids, " centos "):
		return "sudo dnf install git"
	case strings.Contains(ids, " arch "):
		return "sudo pacman -S git"
	case strings.Contains(ids, " alpine "):
		return "sudo apk add git"
	case strings.Contains(ids, " suse "), strings.Contains(ids, " opensuse "):
		return "sudo zypper install git"
	}
	return "install a current git from https://git-scm.com/downloads"
}

// Hint is UpgradeHint for the system squadrn runs on.
func Hint() string { return UpgradeHint(runtime.GOOS, readOSRelease()) }

func readOSRelease() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	return string(b)
}

func parseOSRelease(s string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok {
			out[k] = strings.ToLower(strings.Trim(v, `"'`))
		}
	}
	return out
}
