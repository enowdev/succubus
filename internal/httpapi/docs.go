package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/enowdev/succubus/assets"
	"github.com/enowdev/succubus/docs"
)

// docsList handles GET /api/docs — the table of contents.
func (s *Server) docsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, docs.List())
}

// docsGet handles GET /api/docs/{id} — one section's markdown.
//
// The placeholder binary path in the committed markdown is replaced with this
// machine's real path, so every command and config snippet is ready to paste
// rather than ready to edit. The files on disk keep the placeholder, since a
// reader on GitHub has no path to substitute.
//
// Served as text/markdown rather than JSON so an agent can read it with a plain
// fetch, and so a browser shows something sensible if you open it directly.
func (s *Server) docsGet(w http.ResponseWriter, r *http.Request) {
	body, err := docs.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write([]byte(assets.ResolvePaths(body, binaryPath())))
}

var (
	binOnce sync.Once
	binPath string
)

// binaryPath is this executable's resolved absolute path.
func binaryPath() string {
	binOnce.Do(func() {
		p, err := os.Executable()
		if err != nil {
			return
		}
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		binPath = p
	})
	return binPath
}
