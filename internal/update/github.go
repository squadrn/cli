package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/squadrn/squadrn/internal/fsx"
)

// DefaultAPI is the GitHub REST API. Releases of a public repository need no token; one only
// raises the rate limit.
const DefaultAPI = "https://api.github.com"

var (
	ErrNotFound    = errors.New("not found")
	ErrRateLimited = errors.New("GitHub API rate limit reached")
)

// GitHub reads releases through the REST API. The zero value uses DefaultAPI without a token
// and http.DefaultTransport (proxy settings from the environment apply).
type GitHub struct {
	API    string
	Token  string
	Client *http.Client
}

// Asset is a file attached to a release: its public download URL and its API URL (which also
// serves draft releases, given a token with push access).
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	APIURL string `json:"url"`
}

// ReleaseInfo is a release as the API describes it.
type ReleaseInfo struct {
	Tag        string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Body       string  `json:"body"`
	Assets     []Asset `json:"assets"`
}

// Asset returns the asset called name; ok is false when the release has none.
func (r ReleaseInfo) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

func (g GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (g GitHub) api() string {
	if g.API == "" {
		return DefaultAPI
	}
	return strings.TrimSuffix(g.API, "/")
}

// Releases lists the newest releases of repo (owner/name).
func (g GitHub) Releases(ctx context.Context, repo string) ([]Release, error) {
	list, err := g.list(ctx, repo)
	if err != nil {
		return nil, err
	}
	rels := make([]Release, len(list))
	for i, r := range list {
		rels[i] = Release{Tag: r.Tag, Prerelease: r.Prerelease, Draft: r.Draft}
	}
	return rels, nil
}

func (g GitHub) list(ctx context.Context, repo string) ([]ReleaseInfo, error) {
	var list []ReleaseInfo
	err := g.getJSON(ctx, "/repos/"+repo+"/releases?per_page=50", &list)
	return list, err
}

// Release looks up one release of repo by tag. A draft is only listed, and only to a token
// with push access (the release workflow tests its drafts this way).
func (g GitHub) Release(ctx context.Context, repo, tag string) (ReleaseInfo, error) {
	var r ReleaseInfo
	err := g.getJSON(ctx, "/repos/"+repo+"/releases/tags/"+url.PathEscape(tag), &r)
	if !errors.Is(err, ErrNotFound) || g.Token == "" {
		return r, err
	}
	list, lerr := g.list(ctx, repo)
	if lerr != nil {
		return r, err
	}
	for _, rel := range list {
		if rel.Tag == tag {
			return rel, nil
		}
	}
	return r, err
}

func (g GitHub) getJSON(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.api()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusError(resp); err != nil {
		return err
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v); err != nil {
		return fmt.Errorf("unexpected answer from %s: %w", req.URL.Host, err)
	}
	return nil
}

// Download saves asset a as dst, at most max bytes. Without a token it uses the public URL and
// sends nothing; with one it goes through the API (drafts too). Redirects to other hosts never
// carry the token.
func (g GitHub) Download(ctx context.Context, a Asset, dst string, max int64) error {
	rawURL := a.URL
	if g.Token != "" && a.APIURL != "" {
		rawURL = a.APIURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if rawURL == a.APIURL {
		req.Header.Set("Accept", "application/octet-stream")
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusError(resp); err != nil {
		return err
	}
	if err := fsx.WriteFile(dst, io.LimitReader(resp.Body, max+1), 0o644); err != nil {
		return err
	}
	if fi, err := os.Stat(dst); err == nil && fi.Size() > max {
		_ = os.Remove(dst)
		return fmt.Errorf("%s is larger than %d bytes", rawURL, max)
	}
	return nil
}

func statusError(resp *http.Response) error {
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		msg := ""
		if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
			var unix int64
			if _, err := fmt.Sscan(reset, &unix); err == nil {
				msg = " until " + time.Unix(unix, 0).Format("15:04")
			}
		}
		return fmt.Errorf("%w%s (set GH_TOKEN or GITHUB_TOKEN to raise it)", ErrRateLimited, msg)
	}
	return fmt.Errorf("%s: HTTP %d", resp.Request.URL.Host, resp.StatusCode)
}
