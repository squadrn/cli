//go:build !windows

package proc

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestOutputReturnsStdoutAndStderrOnFailure(t *testing.T) {
	out, err := Output(context.Background(), Options{}, "sh", "-c", `echo '{"ok":false}'; echo boom >&2; exit 3`)
	if string(out) != "{\"ok\":false}\n" {
		t.Errorf("stdout = %q", out)
	}
	if err == nil || !strings.Contains(err.Error(), "boom") || ExitCode(err) != 3 {
		t.Fatalf("err = %v (exit %d)", err, ExitCode(err))
	}
}

func TestDeadlineKillsGrandchildrenHoldingThePipe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The child forks a sleeper that inherits stdout; without group kill + WaitDelay,
	// Output would block for the full 30 s.
	_, err := Output(ctx, Options{}, "sh", "-c", "sleep 30 & sleep 30")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("took %v; the deadline did not hold", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestOptionsDirEnvStdin(t *testing.T) {
	out, err := Output(context.Background(), Options{Dir: "/", Env: []string{"X=1"}, Stdin: strings.NewReader("in")},
		"sh", "-c", `printf '%s %s ' "$PWD" "$X"; cat`)
	if err != nil || string(out) != "/ 1 in" {
		t.Fatalf("out = %q, %v", out, err)
	}
}

// A client answers what the program prints while it runs: stdin is held open until the reply
// arrives on the Stdout tee.
func TestOptionsStdoutTee(t *testing.T) {
	pr, pw := io.Pipe()
	tee := &lineWatcher{want: "ready", seen: make(chan struct{})}
	go func() {
		<-tee.seen
		_, _ = pw.Write([]byte("go\n"))
		_ = pw.Close()
	}()
	out, err := Output(context.Background(), Options{Stdin: pr, Stdout: tee}, "sh", "-c", `echo ready; read x; echo "$x"`)
	if err != nil || string(out) != "ready\ngo\n" {
		t.Fatalf("out = %q, %v", out, err)
	}
}

type lineWatcher struct {
	want string
	seen chan struct{}
	buf  strings.Builder
	done bool
}

func (w *lineWatcher) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if !w.done && strings.Contains(w.buf.String(), w.want+"\n") {
		w.done = true
		close(w.seen)
	}
	return len(p), nil
}

func TestExitCodeOfNonExitError(t *testing.T) {
	if ExitCode(errors.New("x")) != -1 {
		t.Fatal("non-exit error must map to -1")
	}
}

func TestShellQuoteRoundTrips(t *testing.T) {
	argv := []string{"/path with space/squadrn", "-a", "it's", "--resume", `$(rm -rf ~)`, ""}
	script := "for a in " + ShellQuote(argv...) + "; do printf '[%s]\n' \"$a\"; done"
	got, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "[/path with space/squadrn]\n[-a]\n[it's]\n[--resume]\n[$(rm -rf ~)]\n[]\n"
	if string(got) != want {
		t.Fatalf("shell saw:\n%s", got)
	}
}
