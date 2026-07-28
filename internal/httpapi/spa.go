package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// MountSPA serves the built dashboard at the root, falling back to index.html
// so client-side routes survive a hard refresh. API routes are registered on
// more specific patterns, so ServeMux gives them precedence automatically.
func (s *Server) MountSPA(dist fs.FS) {
	fileServer := http.FileServerFS(dist)

	s.Mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "." {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err != nil {
			// A missing asset is a genuine 404. Falling back to the SPA shell
			// here would hand the browser HTML where it expects JS or CSS, and
			// the resulting parse error hides the real problem.
			if strings.HasPrefix(p, "assets/") || path.Ext(p) != "" {
				http.NotFound(w, r)
				return
			}
			// Unknown route: hand the SPA shell over and let the router decide.
			http.ServeFileFS(w, r, dist, "index.html")
			return
		}
		// Vite emits content-hashed asset filenames, so they can cache forever.
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		fileServer.ServeHTTP(w, r)
	}))
}

// MountDevNotice stands in for the SPA when the binary was built without one,
// pointing the developer at the Vite dev server instead of 404ing.
func (s *Server) MountDevNotice(devURL string) {
	s.Mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!doctype html><meta charset="utf-8">
<title>succubus daemon</title>
<style>body{font:15px/1.6 ui-sans-serif,system-ui;margin:3rem auto;max-width:34rem;padding:0 1rem;color:#111}
a{color:#7c3aed}code{background:#f4f4f5;padding:.15rem .4rem;border-radius:4px}
@media(prefers-color-scheme:dark){body{background:#0b0b0e;color:#e5e5e5}code{background:#1c1c22}}</style>
<h1>succubus daemon</h1>
<p>The API is running. The dashboard is not embedded in this build.</p>
<p>Start the dev server with <code>bun run dev</code> in <code>web/</code>, then open
<a href="` + devURL + `">` + devURL + `</a>.</p>
<p>Health check: <a href="/api/health">/api/health</a></p>`))
	}))
}
