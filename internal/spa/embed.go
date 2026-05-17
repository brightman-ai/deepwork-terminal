package spa

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the SPA.
// For any path not matching a static file, serves index.html (SPA client routing).
func Handler() http.Handler {
	dist, _ := fs.Sub(distFS, "dist")
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip leading slash to get relative path
		relPath := r.URL.Path
		if len(relPath) > 0 && relPath[0] == '/' {
			relPath = relPath[1:]
		}
		// Try to serve the exact file
		f, err := dist.Open(relPath)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
