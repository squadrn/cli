package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squadrn/squadrn/internal/config"
)

func TestGitHub(t *testing.T) {
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.URL.Path+"="+r.Header.Get("Authorization"))
		host := "http://" + r.Host
		switch r.URL.Path {
		case "/repos/o/r/releases":
			_, _ = w.Write([]byte(`[{"tag_name":"v2.0.0-rc.1","prerelease":true},{"tag_name":"v1.0.0"},
				{"tag_name":"v3.0.0","draft":true,"assets":[{"name":"d","url":"` + host + `/api/assets/9"}]}]`))
		case "/repos/o/r/releases/tags/v1.0.0":
			_, _ = w.Write([]byte(`{"tag_name":"v1.0.0","body":"notes","assets":[{"name":"a.tar.gz","browser_download_url":"` + host + `/dl/a","url":"` + host + `/api/assets/1"}]}`))
		case "/repos/o/limited/releases":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "1700000000")
			w.WriteHeader(http.StatusForbidden)
		case "/repos/o/broken/releases":
			_, _ = w.Write([]byte(`<html>`))
		case "/repos/o/denied/releases":
			w.WriteHeader(http.StatusForbidden)
		case "/dl/a":
			_, _ = w.Write([]byte("archive"))
		case "/api/assets/1", "/api/assets/9":
			if r.Header.Get("Accept") != "application/octet-stream" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte("via api"))
		case "/dl/big":
			_, _ = w.Write([]byte(strings.Repeat("x", 100)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	anon := GitHub{API: srv.URL + "/"}
	g := GitHub{API: srv.URL, Token: "tok"}

	rels, err := anon.Releases(ctx, "o/r")
	if err != nil || len(rels) != 3 || Newest(rels, config.Stable) != "v1.0.0" || Newest(rels, config.Beta) != "v2.0.0-rc.1" {
		t.Fatalf("Releases = %+v, %v", rels, err)
	}
	rel, err := anon.Release(ctx, "o/r", "v1.0.0")
	a, ok := rel.Asset("a.tar.gz")
	if _, missing := rel.Asset("missing"); err != nil || rel.Body != "notes" || !ok || missing {
		t.Fatalf("Release = %+v, %v", rel, err)
	}
	if _, err := anon.Release(ctx, "o/r", "v3.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a draft without a token: %v", err)
	}
	draft, err := g.Release(ctx, "o/r", "v3.0.0")
	if err != nil || !draft.Draft {
		t.Fatalf("a draft with a token: %+v, %v", draft, err)
	}
	if _, err := g.Release(ctx, "o/r", "v9.9.9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing tag: %v", err)
	}
	if _, err := g.Release(ctx, "o/limited", "v1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing tag, list failing: %v", err)
	}
	if _, err := g.Releases(ctx, "o/limited"); !errors.Is(err, errRateLimited) || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Fatalf("rate limited: %v", err)
	}
	if _, err := g.Releases(ctx, "o/broken"); err == nil || !strings.Contains(err.Error(), "unexpected answer") {
		t.Fatalf("broken JSON: %v", err)
	}
	if _, err := g.Releases(ctx, "o/denied"); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("denied: %v", err)
	}

	dir := t.TempDir()
	if err := anon.Download(ctx, a, filepath.Join(dir, "pub"), 1<<20); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "pub")); string(b) != "archive" {
		t.Fatalf("public download %q", b)
	}
	if err := g.Download(ctx, a, filepath.Join(dir, "api"), 1<<20); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "api")); string(b) != "via api" {
		t.Fatalf("API download %q", b)
	}
	if err := anon.Download(ctx, Asset{URL: srv.URL + "/dl/big"}, filepath.Join(dir, "big"), 10); err == nil {
		t.Fatal("an oversized download was accepted")
	}
	if err := anon.Download(ctx, Asset{URL: srv.URL + "/dl/none"}, filepath.Join(dir, "none"), 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing download: %v", err)
	}
	if err := anon.Download(ctx, Asset{URL: "://bad"}, filepath.Join(dir, "bad"), 10); err == nil {
		t.Fatal("a bad download URL was accepted")
	}
	for _, a := range auth {
		if strings.HasPrefix(a, "/dl/") && !strings.HasSuffix(a, "=") {
			t.Errorf("a public download carried a token: %s", a)
		}
	}
	if _, err := (GitHub{API: "://bad"}).Releases(ctx, "o/r"); err == nil {
		t.Fatal("a bad API URL was accepted")
	}
	custom := GitHub{API: srv.URL, Client: srv.Client()}
	if _, err := custom.Releases(ctx, "o/r"); err != nil {
		t.Fatalf("custom client: %v", err)
	}

	srv.Close()
	if _, err := g.Releases(ctx, "o/r"); err == nil || !strings.Contains(err.Error(), "network error") {
		t.Fatalf("network: %v", err)
	}
	if err := anon.Download(ctx, a, filepath.Join(dir, "x"), 10); err == nil || !strings.Contains(err.Error(), "network error") {
		t.Fatalf("network download: %v", err)
	}
	if (GitHub{}).api() != DefaultAPI {
		t.Fatal("default API")
	}
}
