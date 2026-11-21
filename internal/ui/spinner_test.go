package ui

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSpin(t *testing.T) {
	var b strings.Builder
	var mu sync.Mutex
	stop := Spin(writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return b.Write(p) }), "checking")
	time.Sleep(100 * time.Millisecond)
	stop()
	stop()
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(b.String(), "checking") || !strings.HasSuffix(b.String(), "\r\033[K") {
		t.Fatalf("spinner output %q", b.String())
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
