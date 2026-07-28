package httpapi

import (
	"errors"
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
		// Anything under /api that reached here matched no route: a typo, a
		// removed endpoint, or a path with an empty id like /api/plans/.
		//
		// It must not be answered with the SPA shell. A client gets 200 and
		// HTML, tries to parse it as JSON, and reports `invalid character '<'`
		// — an error that says nothing about the endpoint being wrong. An MCP
		// tool call surfaced exactly that, and the real cause took longer to
		// find than it should have.
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, errors.New("no such endpoint"))
			return
		}

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
		// Same rule as the embedded SPA: an unmatched API path answers in JSON,
		// so a client never has to guess whether it hit an endpoint or a page.
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, errors.New("no such endpoint"))
			return
		}
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
