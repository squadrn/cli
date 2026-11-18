package ui

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Spin shows msg with a spinner on w (a terminal) until the returned stop is called.
func Spin(w io.Writer, msg string) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			_, _ = fmt.Fprintf(w, "\r%c %s", frames[i%len(frames)], msg)
			select {
			case <-done:
				_, _ = fmt.Fprint(w, "\r\033[K")
				return
			case <-t.C:
			}
		}
	})
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
		})
	}
}
