// Package web carries the built dashboard so the daemon ships as one binary.
//
// dist/ is produced by `bun run build` (see the Makefile). A placeholder
// index.html is committed so `go build` works on a fresh clone before the
// frontend has ever been built — without it the //go:embed directive fails.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built SPA rooted at dist/, and whether a real build is
// present. A placeholder-only tree reports false so the daemon can point the
// developer at the Vite dev server instead of serving a stub.
func Dist() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	// Vite emits hashed assets into dist/assets; the placeholder has none.
	entries, err := fs.ReadDir(sub, "assets")
	if err != nil || len(entries) == 0 {
		return sub, false
	}
	return sub, true
}
