// Package webui serves the embedded Vite-built web UI.
//
// The dist/ directory is populated by a build stage in apps/api/Dockerfile
// that runs `npm run build` in apps/web/ and copies the output here. For
// `go build` outside of Docker we ship a minimal placeholder index.html so
// the embed always succeeds.
package webui

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// distRoot returns an fs.FS rooted at the embedded dist directory so
// http.FileServer doesn't see the leading "dist/" segment in URLs.
func distRoot() (fs.FS, error) {
	return fs.Sub(embedded, "dist")
}

// Handler returns an http.Handler that serves the embedded UI at /.
//
// Behavior:
//   - Static asset requests (anything with a known extension) are served
//     directly from the embedded fs and get long-lived cache headers when
//     they live under /assets/ (Vite hashes the filenames).
//   - Anything else falls back to index.html so React Router can take
//     over (deep links like /fleets/homelab/proxy-hosts/new work on hard
//     refresh).
//
// Requests under /api or /healthz are forwarded to apiHandler unchanged
// so this handler can be mounted at the root.
func Handler(apiHandler http.Handler) http.Handler {
	root, err := distRoot()
	if err != nil {
		// embed always succeeds at build time; if it doesn't, fail loud.
		panic(err)
	}
	fileSrv := http.FileServer(http.FS(root))

	indexBytes, err := fs.ReadFile(root, "index.html")
	if err != nil {
		// Same reasoning — placeholder index.html ships in the repo.
		panic(err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path

		// API endpoints take priority and are fully delegated. /metrics
		// is also forwarded so Prometheus scrapes don't fall into the
		// SPA fallback below.
		if strings.HasPrefix(p, "/api/") || p == "/healthz" || p == "/metrics" {
			apiHandler.ServeHTTP(w, r)
			return
		}

		// Cache-bust hashed assets aggressively; everything else stays
		// uncached so updates to index.html roll out instantly.
		if strings.HasPrefix(p, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		// Try to serve a real file first.
		if hasFile(root, p) {
			fileSrv.ServeHTTP(w, r)
			return
		}

		// SPA fallback for unknown routes.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexBytes)
	})
}

// hasFile returns true if the given URL path corresponds to an existing
// (non-directory) file in the embedded fs.
func hasFile(root fs.FS, urlPath string) bool {
	if urlPath == "" || urlPath == "/" {
		return false
	}
	clean := strings.TrimPrefix(path.Clean(urlPath), "/")
	if clean == "" {
		return false
	}
	info, err := fs.Stat(root, clean)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false
		}
		return false
	}
	return !info.IsDir()
}
