// Package webui serves the web page built from web/ (see scripts/build.sh).
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the built page, falling back to index.html for page routes
// such as /signin.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(root, p); err != nil {
			if strings.HasPrefix(p, "api/") || strings.Contains(path.Base(p), ".") {
				http.NotFound(w, r)
				return
			}
			p = "index.html"
		}
		if p == "index.html" {
			b, err := fs.ReadFile(root, "index.html")
			if err != nil {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Write([]byte("The web page is not built into this binary. Run scripts/build.sh.\n"))
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			w.Write(b)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
