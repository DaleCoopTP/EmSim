// Package web embeds the built SPA (ADR-009: "отдаётся тем же процессом")
// into the emsim binary, so "emsim api" needs no separate static-file
// deployment step. web/dist is populated by "npm run build" — see this
// directory's README.md and the Makefile's web-build target — and holds
// only a checked-in .gitkeep placeholder otherwise, so `go build` never
// needs Node: cmd/emsim/static.go serves a clear error instead of a
// built SPA until dist is actually populated.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// DistFS is web/dist re-rooted at its own directory: distFS's paths all
// carry a "dist/" prefix (embed.FS keeps the directory named in the
// //go:embed directive), which callers serving these files as a
// filesystem root do not want.
var DistFS fs.FS = mustSub(distFS, "dist")

func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		// Unreachable: "dist" is the exact directory //go:embed above
		// names, so fs.Sub can only fail here if that embed itself
		// failed, which would already be a compile error.
		panic(err)
	}
	return sub
}
