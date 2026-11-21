//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGeminiGuardAgainstMockAPI runs the real Gemini CLI, when it is on PATH, against a local
// mock of the Gemini API (GOOGLE_GEMINI_BASE_URL, a fake API key, a throwaway HOME and
// GEMINI_CLI_HOME), as ADR 0013's probe did: the model asks for `git push -f origin main`, and
// squadrn's BeforeTool hook, written by `srn account add`, must block it. No real account and no
// network are used. Skipped without gemini.
func TestGeminiGuardAgainstMockAPI(t *testing.T) {
	gemini, err := exec.LookPath("gemini")
	if err != nil {
		t.Skip("gemini is not on PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix only: the mock run uses sh")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	bin := filepath.Join(root, "bin")
	for _, d := range []string{home, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "squadrn"), "./cmd/squadrn")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	// Nothing may reach the login Keychain. The minimal profile allows yolo mode; its policy file
	// is loaded all the same.
	writeMode(t, filepath.Join(bin, "security"), "#!/bin/sh\nexit 1\n", 0o755)
	repoRoot, _ := filepath.Abs("..")

	api := newMockGemini(t)
	env := []string{
		"HOME=" + home, "PATH=" + bin + string(os.PathListSeparator) + filepath.Dir(gemini) + string(os.PathListSeparator) + "/usr/bin:/bin:/usr/sbin:/sbin",
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"SQUADRN_MARKETPLACE=" + repoRoot, "GEMINI_API_KEY=fake-key", "GOOGLE_GEMINI_BASE_URL=" + api.url,
		"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "NO_BROWSER=true",
	}
	run := func(dir, name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		t.Logf("$ %s %s\n%s", name, strings.Join(args, " "), out)
		if err != nil && name != "squadrn" {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".gemini", "settings.json"), `{"security":{"auth":{"selectedType":"gemini-api-key"}}}`)
	run(home, filepath.Join(bin, "squadrn"), "account", "add", "gemini/probe", "--profile", "minimal", "--no-login", "-y")
	settings := read(t, filepath.Join(home, ".gemini-probe", ".gemini", "settings.json"))
	if !strings.Contains(settings, "squadrn-guard") {
		t.Fatalf("no guard hook in the account:\n%s", settings)
	}
	repo := filepath.Join(root, "repo")
	run(root, "git", "init", "-q", "-b", "main", repo)
	run(repo, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x")
	run(repo, filepath.Join(bin, "squadrn"), "gemini/probe", "--approval-mode", "yolo", "--skip-trust", "-p", "push it")
	if !api.sawBlock() {
		t.Fatalf("the model was not told the push was blocked; requests:\n%s", api.log())
	}
}

// mockGemini answers generateContent: first with a run_shell_command call, then with text.
type mockGemini struct {
	url    string
	mu     sync.Mutex
	turns  int
	bodies []string
}

func newMockGemini(t *testing.T) *mockGemini {
	m := &mockGemini{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m.url = "http://" + ln.Addr().String()
	srv := &http.Server{Handler: m, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return m
}

func (m *mockGemini) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	m.mu.Lock()
	m.bodies = append(m.bodies, r.URL.Path+" "+string(b))
	// The agent's turns stream; plain generateContent calls are side requests (model routing).
	isTurn := strings.Contains(r.URL.Path, "streamGenerateContent")
	turn := m.turns
	if isTurn {
		m.turns++
	}
	m.mu.Unlock()
	if strings.Contains(r.URL.Path, "countTokens") {
		_ = json.NewEncoder(w).Encode(map[string]any{"totalTokens": 1})
		return
	}
	if !isTurn && !strings.Contains(r.URL.Path, "generateContent") {
		_ = json.NewEncoder(w).Encode(map[string]any{})
		return
	}
	part := map[string]any{"text": "done"}
	if isTurn && turn == 0 {
		part = map[string]any{"functionCall": map[string]any{"name": "run_shell_command", "args": map[string]any{"command": "git push -f origin main", "description": "push"}}}
	}
	resp := map[string]any{
		"candidates":    []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{part}}, "finishReason": "STOP", "index": 0}},
		"usageMetadata": map[string]any{"promptTokenCount": 1, "candidatesTokenCount": 1, "totalTokenCount": 2},
	}
	body, _ := json.Marshal(resp)
	if strings.Contains(r.URL.Path, "stream") {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + string(body) + "\n\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// sawBlock: a request after the tool call carries the guard's reason back to the model.
func (m *mockGemini) sawBlock() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.bodies {
		if strings.Contains(b, "squadrn guard") {
			return true
		}
	}
	return false
}

func (m *mockGemini) log() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.bodies, "\n")
}
