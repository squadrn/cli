package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/squadrn/squadrn/internal/proc"
)

// request is one JSON-RPC call to `codex app-server` (stdio, one JSON message per line).
type request struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

// reply is the answer to one request: Result, or Err.
type reply struct {
	Result json.RawMessage
	Err    error
}

// appServer runs `codex app-server` for configDir, performs the initialize handshake, sends reqs
// and returns their replies in order. The server is stopped once every reply has arrived; ctx
// bounds the whole exchange.
func (c CLI) appServer(ctx context.Context, configDir string, reqs ...request) ([]reply, error) {
	if err := c.prepare(configDir); err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	s := &session{replies: make([]reply, len(reqs)), pending: len(reqs) + 1, initialized: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer func() { _ = pw.Close() }() // EOF: the server exits
		enc := json.NewEncoder(pw)
		if enc.Encode(map[string]any{"id": 0, "method": "initialize", "params": map[string]any{
			"clientInfo": map[string]string{"name": "squadrn", "title": "Squadrn", "version": "1"},
		}}) != nil {
			return
		}
		select {
		case <-s.initialized:
		case <-ctx.Done():
			return
		}
		if enc.Encode(map[string]string{"method": "initialized"}) != nil {
			return
		}
		for i, r := range reqs {
			msg := map[string]any{"id": i + 1, "method": r.Method}
			if r.Params != nil {
				msg["params"] = r.Params
			}
			if enc.Encode(msg) != nil {
				return
			}
		}
		select {
		case <-s.done:
		case <-ctx.Done():
		}
	}()
	_, err := proc.Output(ctx, proc.Options{Env: c.env(configDir), Stdin: pr, Stdout: s}, c.Bin, "app-server")
	_ = pr.CloseWithError(io.ErrClosedPipe)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initErr != nil {
		return nil, fmt.Errorf("codex app-server: initialize: %w", s.initErr)
	}
	if s.pending > 0 {
		if err == nil {
			err = errors.New("exited before answering")
		}
		return nil, fmt.Errorf("codex app-server: %w", err)
	}
	return s.replies, nil
}

// session collects the replies from the server's stdout as it is written.
type session struct {
	mu          sync.Mutex
	buf         []byte
	replies     []reply
	pending     int
	gotInit     bool
	initErr     error
	initialized chan struct{}
	done        chan struct{}
}

func (s *session) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		s.line(s.buf[:i])
		s.buf = s.buf[i+1:]
	}
}

// line handles one message; notifications and requests from the server are ignored.
func (s *session) line(b []byte) {
	var m struct {
		ID     *int64          `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if json.Unmarshal(b, &m) != nil || m.ID == nil || m.Method != "" || s.pending == 0 {
		return
	}
	id := *m.ID
	var err error
	if m.Error != nil {
		err = m.Error
	}
	switch {
	case id == 0:
		if s.gotInit {
			return
		}
		s.gotInit, s.initErr = true, err
		s.pending--
		close(s.initialized)
		if err != nil {
			s.pending = 0
			close(s.done)
			return
		}
	case id >= 1 && id <= int64(len(s.replies)):
		if s.replies[id-1].Result != nil || s.replies[id-1].Err != nil {
			return
		}
		s.replies[id-1] = reply{Result: m.Result, Err: err}
		if s.replies[id-1].Result == nil && err == nil {
			s.replies[id-1].Result = json.RawMessage("null")
		}
		s.pending--
	default:
		return
	}
	if s.pending == 0 {
		close(s.done)
	}
}
