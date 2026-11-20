//go:build e2e

package e2e

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/squadrn/squadrn/internal/fakeagent"
	"github.com/squadrn/squadrn/internal/release"
)

const (
	module = "github.com/squadrn/squadrn"
	// version is the release the harness builds and installs; newer is what the fake release
	// server offers to self-update --check.
	version = "0.99.0"
	newer   = "v0.99.1"
	notes   = "e2e release notes"
)

// fakeAPI is the base URL of the fake GitHub REST API; the binary is built to use it.
var fakeAPI string

func TestMain(m *testing.M) {
	fakeagent.Main()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(1)
	}
	fakeAPI = "http://" + ln.Addr().String()
	srv := &http.Server{Handler: http.HandlerFunc(serveReleases), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	code := m.Run()
	_ = srv.Close()
	os.Exit(code)
}

// serveReleases is the part of the GitHub REST API self-update --check reads: one newer stable
// release of squadrn/squadrn.
func serveReleases(w http.ResponseWriter, r *http.Request) {
	rel := map[string]any{"tag_name": newer, "prerelease": false, "draft": false, "body": notes, "assets": []any{}}
	switch r.URL.Path {
	case "/repos/squadrn/squadrn/releases":
		_ = json.NewEncoder(w).Encode([]any{rel})
	case "/repos/squadrn/squadrn/releases/tags/" + newer:
		_ = json.NewEncoder(w).Encode(rel)
	default:
		http.NotFound(w, r)
	}
}

// sandbox is a throwaway machine: HOME, XDG dirs, fake agent CLIs first on PATH, a private tmux
// server and git without the developer's config. Nothing outside root is written.
type sandbox struct {
	t        *testing.T
	root     string
	home     string
	repoRoot string
	fakeBin  string
	env      []string
	path     []string
	log      *bytes.Buffer
}

// dropEnv lists variables of the developer's environment that must not reach the sandbox: they
// would point squadrn, git or the agent CLI at real accounts, repositories or hooks.
var dropEnv = regexp.MustCompile(`(?i)^(HOME|USERPROFILE|XDG_[A-Z_]+|GIT_[A-Z0-9_]+|CLAUDE_[A-Z_]+|CODEX_[A-Z_]+|ANTHROPIC_[A-Z_]+|OPENAI_[A-Z_]+|SQUADRN_[A-Z_]+|FAKE_[A-Z_]+|TMUX|TMUX_[A-Z_]+|GH_[A-Z_]+|GITHUB_TOKEN|SSH_AUTH_SOCK|PATH)=`)

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	s := &sandbox{
		t: t, root: root, home: filepath.Join(root, "home"), repoRoot: filepath.Dir(wd),
		fakeBin: filepath.Join(root, "fakebin"), log: &bytes.Buffer{},
	}
	for _, d := range []string{s.fakeBin, filepath.Join(s.home, ".claude")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.installFakes()

	for _, kv := range os.Environ() {
		if !dropEnv.MatchString(kv) {
			s.env = append(s.env, kv)
		}
	}
	realHome, _ := os.UserHomeDir()
	s.path = []string{s.fakeBin, filepath.Join(s.home, ".local", "bin")}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		// Tools installed for the developer (a real squadrn or claude in ~/.local/bin) stay out.
		if rel, err := filepath.Rel(realHome, d); realHome != "" && err == nil && !strings.HasPrefix(rel, "..") {
			continue
		}
		s.path = append(s.path, d)
	}
	gitconfig := filepath.Join(root, "gitconfig")
	write(t, gitconfig, "[user]\n\tname = e2e\n\temail = e2e@example.invalid\n[init]\n\tdefaultBranch = main\n")
	s.setenv(
		"PATH="+strings.Join(s.path, string(os.PathListSeparator)),
		"HOME="+s.home,
		"USERPROFILE="+s.home,
		"XDG_CONFIG_HOME="+filepath.Join(s.home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(s.home, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(s.home, ".local", "state"),
		"XDG_CACHE_HOME="+filepath.Join(s.home, ".cache"),
		"GIT_CONFIG_GLOBAL="+gitconfig,
		"GIT_CONFIG_NOSYSTEM=1",
		"SQUADRN_NO_UPDATE_CHECK=1",
		"NO_COLOR=1",
	)
	if runtime.GOOS != "windows" {
		// A private tmux server; its socket path must stay short (macOS limit ~104 bytes).
		sock, err := os.MkdirTemp("/tmp", "srne")
		if err != nil {
			t.Fatal(err)
		}
		s.setenv("TMUX_TMPDIR=" + sock)
		t.Cleanup(func() {
			if tmux, err := exec.LookPath("tmux"); err == nil {
				kill := exec.Command(tmux, "kill-server")
				kill.Env = s.env
				_ = kill.Run()
			}
			_ = os.RemoveAll(sock)
		})
	}
	return s
}

// installFakes puts every fake agent CLI (internal/fakeagent) first on PATH, plus a failing
// `security` on macOS so nothing can reach the login Keychain.
func (s *sandbox) installFakes() {
	self, err := os.Executable()
	if err != nil {
		s.t.Fatal(err)
	}
	for _, name := range fakeagent.Names() {
		if runtime.GOOS == "windows" {
			// The fake is this test binary, started under the CLI's name.
			copyFile(s.t, self, filepath.Join(s.fakeBin, name+".exe"), 0o755)
			continue
		}
		wrapper := "#!/bin/sh\nFAKE_AGENT=" + name + " exec '" + strings.ReplaceAll(self, "'", `'\''`) + "' \"$@\"\n"
		writeMode(s.t, filepath.Join(s.fakeBin, name), wrapper, 0o755)
	}
	if runtime.GOOS != "windows" {
		writeMode(s.t, filepath.Join(s.fakeBin, "security"), "#!/bin/sh\necho 'security: blocked in the e2e sandbox' >&2\nexit 1\n", 0o755)
		writeMode(s.t, filepath.Join(s.fakeBin, "pnpm"), "#!/bin/sh\necho \"pnpm $* in $(pwd -P)\"\n", 0o755)
	} else {
		write(s.t, filepath.Join(s.fakeBin, "pnpm.cmd"), "@echo pnpm %* in %CD%\r\n")
	}
}

func (s *sandbox) setenv(kvs ...string) {
	for _, kv := range kvs {
		k := strings.ToUpper(kv[:strings.IndexByte(kv, '=')+1])
		s.env = slices.DeleteFunc(s.env, func(e string) bool { return strings.HasPrefix(strings.ToUpper(e), k) })
		s.env = append(s.env, kv)
	}
}

// lookPath finds name on the sandbox PATH (exec.Command would use the test's own PATH).
func (s *sandbox) lookPath(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{".exe", ".cmd", ".bat", ""}
	}
	for _, d := range s.path {
		for _, ext := range exts {
			p := filepath.Join(d, name+ext)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}
	return name
}

// opt changes one command run: its directory, stdin, extra environment.
type opt struct {
	dir   string
	stdin string
	env   []string
}

type result struct {
	s            *sandbox
	t            *testing.T
	line         string
	code         int
	stdout, errs string
}

// run runs a command in the sandbox and records it in the transcript.
func (s *sandbox) run(t *testing.T, o opt, name string, args ...string) *result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.lookPath(name), args...)
	cmd.Dir = o.dir
	if cmd.Dir == "" {
		cmd.Dir = s.root
	}
	cmd.Env = append(append([]string{}, s.env...), o.env...)
	cmd.Stdin = strings.NewReader(o.stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	r := &result{s: s, t: t, line: strings.TrimSpace(name + " " + strings.Join(args, " ")), stdout: stdout.String(), errs: stderr.String()}
	switch {
	case cmd.ProcessState != nil:
		r.code = cmd.ProcessState.ExitCode()
	case err != nil:
		r.code = -1
		r.errs += "\n[harness] " + err.Error()
	}
	rel, _ := filepath.Rel(s.root, cmd.Dir)
	fmt.Fprintf(s.log, "$ (%s) %s", filepath.ToSlash(rel), r.line)
	if len(o.env) > 0 {
		fmt.Fprintf(s.log, "  [env %s]", strings.Join(o.env, " "))
	}
	fmt.Fprintf(s.log, "\n[exit %d]\n", r.code)
	if r.stdout != "" {
		fmt.Fprintf(s.log, "%s", indent(r.stdout, "  | "))
	}
	if r.errs != "" {
		fmt.Fprintf(s.log, "%s", indent(r.errs, "  ! "))
	}
	return r
}

func indent(s, prefix string) string {
	s = strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix) + "\n"
}

// fail stops the test and prints the whole transcript so far.
func (s *sandbox) fail(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Fatalf("%s\n\n--- transcript ---\n%s", fmt.Sprintf(format, args...), s.log.String())
}

func (r *result) exit(want int) *result {
	r.t.Helper()
	if r.code != want {
		r.s.fail(r.t, "%s: exit %d, want %d", r.line, r.code, want)
	}
	return r
}

// out asserts that stdout matches each regexp (multi-line mode).
func (r *result) out(res ...string) *result {
	r.t.Helper()
	for _, re := range res {
		if !regexp.MustCompile("(?m)" + re).MatchString(norm(r.stdout)) {
			r.s.fail(r.t, "%s: stdout does not match %q", r.line, re)
		}
	}
	return r
}

// err asserts that stderr matches each regexp.
func (r *result) err(res ...string) {
	r.t.Helper()
	for _, re := range res {
		if !regexp.MustCompile("(?m)" + re).MatchString(norm(r.errs)) {
			r.s.fail(r.t, "%s: stderr does not match %q", r.line, re)
		}
	}
}

// noOut asserts that stdout matches none of the regexps.
func (r *result) noOut(res ...string) {
	r.t.Helper()
	for _, re := range res {
		if regexp.MustCompile("(?m)" + re).MatchString(norm(r.stdout)) {
			r.s.fail(r.t, "%s: stdout unexpectedly matches %q", r.line, re)
		}
	}
}

func norm(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func (s *sandbox) srn(t *testing.T, args ...string) *result {
	t.Helper()
	return s.run(t, opt{}, "srn", args...)
}

func (s *sandbox) srnIn(t *testing.T, dir string, args ...string) *result {
	t.Helper()
	return s.run(t, opt{dir: dir}, "srn", args...)
}

func (s *sandbox) git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return s.run(t, opt{dir: dir}, "git", args...).exit(0).stdout
}

// check fails the test with the transcript when ok is false.
func (s *sandbox) check(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		s.fail(t, format, args...)
	}
}

func (s *sandbox) exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// waitFor polls until the file matches the regexp; agents in panes start slowly on CI.
func (s *sandbox) waitFor(t *testing.T, path, re string) {
	t.Helper()
	rx := regexp.MustCompile("(?m)" + re)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && rx.Match(b) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	b, _ := os.ReadFile(path)
	s.fail(t, "%s: no match for %q within 20s; it holds:\n%s", path, re, b)
}

// buildRelease builds squadrn from this checkout as release vVERSION, laid out like the
// goreleaser archive for this platform, plus a signed checksums.txt and the rendered installers
// in pub/<tag>/. It returns pub (the SQUADRN_INSTALL_BASE) and the rendered installer.
func (s *sandbox) buildRelease(t *testing.T) (pub, installer string) {
	t.Helper()
	tag := "v" + version
	rel := filepath.Join(s.root, "rel")
	pub = filepath.Join(s.root, "pub")
	dir := filepath.Join(pub, tag)
	for _, d := range []string{rel, dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ldflags := fmt.Sprintf("-X %[1]s/internal/cli.Version=%[2]s -X %[1]s/internal/cli.githubAPI=%[3]s", module, version, fakeAPI)
	build := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", filepath.Join(rel, release.BinaryName), "./cmd/squadrn")
	build.Dir = s.repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	copyTree(t, filepath.Join(s.repoRoot, "plugin"), filepath.Join(rel, "plugin"))
	copyTree(t, filepath.Join(s.repoRoot, ".claude-plugin"), filepath.Join(rel, ".claude-plugin"))
	if runtime.GOOS == "windows" {
		copyFile(t, filepath.Join(s.repoRoot, "scripts", "hooks.windows.json"), filepath.Join(rel, "plugin", "hooks", "hooks.json"), 0o644)
	}
	asset := release.AssetName(tag, runtime.GOOS, runtime.GOARCH)
	archive(t, rel, filepath.Join(dir, asset))
	b, err := os.ReadFile(filepath.Join(dir, asset))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	write(t, filepath.Join(dir, release.ChecksumsName), hex.EncodeToString(sum[:])+"  "+asset+"\n")

	// A throwaway signing key: the installer is rendered to trust exactly this key.
	key := filepath.Join(s.root, "relkey")
	keygen := sshKeygen(t)
	for _, args := range [][]string{
		{"-q", "-t", "ed25519", "-N", "", "-C", "e2e", "-f", key},
		{"-Y", "sign", "-q", "-f", key, "-n", "squadrn-release", filepath.Join(dir, release.ChecksumsName)},
	} {
		if out, err := exec.Command(keygen, args...).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen %v: %v\n%s", args, err, out)
		}
	}
	pubkey, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	name := "install.sh"
	if runtime.GOOS == "windows" {
		name = "install.ps1"
	}
	src, err := os.ReadFile(filepath.Join(s.repoRoot, "scripts", name))
	if err != nil {
		t.Fatal(err)
	}
	// The substitutions of scripts/render-install.sh.
	text := strings.NewReplacer("__SQUADRN_REPO__", "squadrn/squadrn", "__SQUADRN_VERSION__", tag).Replace(string(src))
	text = strings.Replace(text, "\n__SQUADRN_SIGNERS__\n", "\n"+strings.TrimSpace(string(pubkey))+"\n", 1)
	installer = filepath.Join(dir, name)
	write(t, installer, text)
	return pub, installer
}

func sshKeygen(t *testing.T) string {
	if p, err := exec.LookPath("ssh-keygen"); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		p := filepath.Join(os.Getenv("ProgramFiles"), "Git", "usr", "bin", "ssh-keygen.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatal("ssh-keygen is needed to sign the test release")
	return ""
}

// archive packs dir into a .tar.gz or .zip (by name), like the release archives.
func archive(t *testing.T, dir, name string) {
	t.Helper()
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var add func(rel string, fi fs.FileInfo, body []byte) error
	var closeAll func() error
	if strings.HasSuffix(name, ".zip") {
		zw := zip.NewWriter(f)
		add = func(rel string, fi fs.FileInfo, body []byte) error {
			h, err := zip.FileInfoHeader(fi)
			if err != nil {
				return err
			}
			h.Name, h.Method = rel, zip.Deflate
			w, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			_, err = w.Write(body)
			return err
		}
		closeAll = zw.Close
	} else {
		gz := gzip.NewWriter(f)
		tw := tar.NewWriter(gz)
		add = func(rel string, fi fs.FileInfo, body []byte) error {
			h, err := tar.FileInfoHeader(fi, "")
			if err != nil {
				return err
			}
			h.Name = rel
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			_, err = tw.Write(body)
			return err
		}
		closeAll = func() error {
			if err := tw.Close(); err != nil {
				return err
			}
			return gz.Close()
		}
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		return add(filepath.ToSlash(rel), fi, body)
	})
	if err == nil {
		err = closeAll()
	}
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, content string) { writeMode(t, path, content, 0o644) }

func writeMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string, mode os.FileMode) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		copyFile(t, p, filepath.Join(dst, rel), fi.Mode().Perm())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
