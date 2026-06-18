package spa

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/brightman-ai/kit/webserve"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the SPA.
// For any path not matching a static file, serves index.html (SPA client routing).
//
// Caching is split so a fresh build is ALWAYS picked up on the next load:
//   - assets/* are content-hashed (e.g. index-D1r1BErK.js) → immutable, cache forever.
//   - index.html (and sw.js / manifest / icons) → no-cache, so the browser revalidates
//     and never pins a stale index.html that references last build's asset hashes. Without
//     this, a normal reload kept serving the old bundle and code fixes never reached the
//     user — only a hard-refresh did.
func Handler() http.Handler {
	dist, _ := fs.Sub(distFS, "dist")
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the exact file; the Vite cache split (content-hashed assets/*
		// immutable, shell no-cache) is the shared webserve SSOT so terminal + pro agree.
		relPath := r.URL.Path
		if f, err := dist.Open(strings.TrimPrefix(relPath, "/")); err == nil {
			f.Close()
			w.Header().Set("Cache-Control", webserve.AssetCacheControl(relPath))
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for client-side routing. NEVER cache it — a stale
		// index.html pins the previous build's asset hashes and silently blocks updates.
		w.Header().Set("Cache-Control", webserve.AssetCacheControl("/"))
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
