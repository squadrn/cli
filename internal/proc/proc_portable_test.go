package proc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as the helper program (SQUADRN_PROC_HELPER selects the behaviour), so
// these tests need no shell and run on every platform.
func TestMain(m *testing.M) {
	switch os.Getenv("SQUADRN_PROC_HELPER") {
	case "":
		os.Exit(m.Run())
	case "fail":
		fmt.Println(`{"ok":false}`)
		fmt.Fprintln(os.Stderr, "boom")
		os.Exit(3)
	case "env":
		b := make([]byte, 64)
		n, _ := os.Stdin.Read(b)
		wd, _ := os.Getwd()
		fmt.Printf("%s|%s|%s", os.Getenv("PROBE"), strings.TrimSpace(string(b[:n])), wd)
	case "orphan":
		// Start a grandchild that inherits stdout and outlives us, then exit at once: only a
		// tree kill (process group, job object) stops the pipe from being held open.
		self, _ := os.Executable()
		child := exec.Command(self)
		child.Env = append(os.Environ(), "SQUADRN_PROC_HELPER=sleep")
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			os.Exit(9)
		}
		fmt.Println(child.Process.Pid)
	case "sleep":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func helper(t *testing.T, mode string) (string, Options) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return self, Options{Env: append(os.Environ(), "SQUADRN_PROC_HELPER="+mode)}
}

func TestPortableOutputOnFailure(t *testing.T) {
	bin, opts := helper(t, "fail")
	out, err := Output(context.Background(), opts, bin)
	if strings.TrimSpace(string(out)) != `{"ok":false}` {
		t.Errorf("stdout = %q", out)
	}
	if err == nil || !strings.Contains(err.Error(), "boom") || ExitCode(err) != 3 {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
}

func TestPortableOptions(t *testing.T) {
	bin, opts := helper(t, "env")
	dir := t.TempDir()
	opts.Env = append(opts.Env, "PROBE=yes")
	opts.Dir = dir
	opts.Stdin = strings.NewReader("in")
	out, err := Output(context.Background(), opts, bin)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(out), "|")
	if len(parts) != 3 || parts[0] != "yes" || parts[1] != "in" || !sameDir(parts[2], dir) {
		t.Fatalf("output %q", out)
	}
}

func sameDir(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

// An orphaned grandchild holding stdout cannot make Output hang: WaitDelay bounds the wait.
func TestPortableOrphanedGrandchildDoesNotHang(t *testing.T) {
	bin, opts := helper(t, "orphan")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := Output(ctx, opts, bin)
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("Output took %v: the orphaned grandchild kept the pipe open", took)
	}
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the bounded pipe wait", err)
	}
}

func TestAlive(t *testing.T) {
	self := os.Getpid()
	if !Alive(self, StartTime(self)) || !Alive(self, "") {
		t.Fatal("this process is not alive")
	}
	if Alive(0, "") || Alive(999999999, "") {
		t.Fatal("a missing process is alive")
	}
	if StartTime(self) != "" && Alive(self, "not the start time") {
		t.Fatal("a reused pid counts as the same process")
	}
}
