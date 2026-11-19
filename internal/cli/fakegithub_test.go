package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
)

// fakeGitHub serves the part of the GitHub REST API self-update uses, from a script's work dir:
// $WORK/api/releases.json lists the releases (tag_name, prerelease, draft, body) and every file
// in $WORK/pub is an asset of each of them (public URL /download/, API URL /api-asset/).
// Requests are logged to $WORK/api/requests.log with the token they carried.
func fakeGitHub(work string) *httptest.Server {
	pub := filepath.Join(work, "pub")
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, err := os.OpenFile(filepath.Join(work, "api", "requests.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = f.WriteString(r.URL.Path + " auth=" + r.Header.Get("Authorization") + "\n")
			_ = f.Close()
		}
		if name, ok := strings.CutPrefix(r.URL.Path, "/download/"); ok {
			http.ServeFile(w, r, filepath.Join(pub, filepath.Base(name)))
			return
		}
		if name, ok := strings.CutPrefix(r.URL.Path, "/api-asset/"); ok {
			if r.Header.Get("Accept") != "application/octet-stream" {
				http.Error(w, "asset metadata", http.StatusBadRequest)
				return
			}
			http.ServeFile(w, r, filepath.Join(pub, filepath.Base(name)))
			return
		}
		if _, err := os.Stat(filepath.Join(work, "api", "ratelimited")); err == nil {
			w.Header().Set("X-RateLimit-Remaining", "0")
			http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
			return
		}
		b, err := os.ReadFile(filepath.Join(work, "api", "releases.json"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		var rels []map[string]any
		if err := json.Unmarshal(b, &rels); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var assets []map[string]string
		entries, _ := os.ReadDir(pub)
		for _, e := range entries {
			assets = append(assets, map[string]string{
				"name": e.Name(), "browser_download_url": "http://" + r.Host + "/download/" + e.Name(), "url": "http://" + r.Host + "/api-asset/" + e.Name(),
			})
		}
		for _, rel := range rels {
			rel["assets"] = assets
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case len(parts) == 4 && parts[0] == "repos" && parts[3] == "releases":
			_ = json.NewEncoder(w).Encode(rels)
		case len(parts) == 6 && parts[0] == "repos" && parts[3] == "releases" && parts[4] == "tags":
			for _, rel := range rels {
				if rel["tag_name"] == parts[5] {
					_ = json.NewEncoder(w).Encode(rel)
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
}
