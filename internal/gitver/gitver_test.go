package gitver

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for out, want := range map[string]Version{
		"git version 2.39.5 (Apple Git-154)": {2, 39, 5},
		"git version 2.47.1.windows.1":       {2, 47, 1},
		"git version 2.36.0.vfs.0.0":         {2, 36, 0},
		"git version 2.45.GIT":               {2, 45, 0},
		"2.43.0.rc1":                         {2, 43, 0},
		"git version 2.31.0\r\n":             {2, 31, 0},
		"git version 3.0.0":                  {3, 0, 0},
		"git version 2.100.2":                {2, 100, 2},
	} {
		got, err := Parse(out)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", out, got, err, want)
		}
	}
	for _, bad := range []string{"", "garbage", "git version x.y"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

func TestAtLeast(t *testing.T) {
	for _, tt := range []struct {
		v    Version
		want bool
	}{
		{Version{2, 31, 0}, true},
		{Version{2, 30, 9}, false},
		{Version{2, 100, 0}, true},
		{Version{3, 0, 0}, true},
		{Version{1, 99, 0}, false},
		{Version{2, 31, 1}, true},
	} {
		if got := tt.v.AtLeast(Min); got != tt.want {
			t.Errorf("%v.AtLeast(%v) = %v", tt.v, Min, got)
		}
	}
}

func TestUpgradeHint(t *testing.T) {
	for _, tt := range []struct{ goos, osRelease, want string }{
		{"darwin", "", "brew install git"},
		{"windows", "", "winget install --id Git.Git -e"},
		{"linux", "ID=ubuntu\nVERSION_ID=\"22.04\"\n", "ppa:git-core/ppa"},
		{"linux", "ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n", "ppa:git-core/ppa"},
		{"linux", "ID=debian\nVERSION_CODENAME=bullseye\n", "-t bullseye-backports"},
		{"linux", "ID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\n", "dnf install git"},
		{"linux", "ID=arch\n", "pacman -S git"},
		{"linux", "ID=alpine\n", "apk add git"},
		{"linux", "ID=opensuse-leap\nID_LIKE=\"suse opensuse\"\n", "zypper"},
		{"linux", "", "git-scm.com"},
	} {
		if got := UpgradeHint(tt.goos, tt.osRelease); !strings.Contains(got, tt.want) {
			t.Errorf("UpgradeHint(%s, %q) = %q, want %q", tt.goos, tt.osRelease, got, tt.want)
		}
	}
}

func TestRequireAndError(t *testing.T) {
	if err := Require(context.Background(), "darwin"); err != nil {
		t.Fatalf("the git running the tests is too old? %v", err)
	}
	e := TooOldError{Have: Version{2, 30, 2}, Hint: "brew install git"}
	if !errors.Is(e, ErrTooOld) || !strings.Contains(e.Error(), "git 2.30.2 is too old; sprawl needs git 2.31 or newer (upgrade: brew install git)") {
		t.Fatalf("error = %v", e)
	}
	t.Setenv("PATH", "")
	if _, err := Detect(context.Background()); err == nil || errors.Is(err, ErrTooOld) {
		t.Fatalf("missing git: %v", err)
	}
}
