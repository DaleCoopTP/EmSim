// Serves the embedded SPA build (web/embed.go's web.DistFS) for every
// public request newPublicHandler's API mux (mounted at "/api/") does not
// claim — ADR-009: "Фронтенд... отдаётся тем же процессом".
package main

import (
	"io/fs"
	"net/http"
	"strings"

	"emsim/internal/platform/httpapi"
)

// hashedAssetPrefix is the directory Vite writes content-hashed build
// output under (web/vite.config.ts's default "assets/") — every filename
// in it already changes when its content does, so it is safe to cache
// forever. Everything else (index.html, favicon.svg, ...) falls back to
// the Cache-Control: no-store every public response already carries
// (httpapi.WithRequestID), so a new deploy is always picked up.
const hashedAssetPrefix = "assets/"

// newSPAHandler serves fsys as a single-page app: a request path naming
// a real file in fsys is served as that file, and everything else
// (react-router client routes like /admin/users, which name no real
// file) falls back to index.html — the same "try_files $uri /index.html"
// shape a plain static-file server needs for client-side routing. When
// fsys holds no index.html at all — the checked-in web/dist/.gitkeep
// placeholder, before "make web-build" or the Dockerfile's Node stage
// has populated it — every request gets the same closed error envelope
// the rest of the API uses, naming the missing build rather than an
// unexplained 404.
func newSPAHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(fsys, "index.html"); err != nil {
			httpapi.WriteError(w, r, httpapi.CodeNotFound,
				`web client is not built: run "make web-build" or use the Docker image`, nil)
			return
		}

		requestPath := strings.TrimPrefix(r.URL.Path, "/")
		if requestPath == "" {
			requestPath = "index.html"
		}
		if strings.HasPrefix(requestPath, hashedAssetPrefix) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if info, err := fs.Stat(fsys, requestPath); err != nil || info.IsDir() {
			r = cloneWithPath(r, "/")
		}
		fileServer.ServeHTTP(w, r)
	})
}

// cloneWithPath returns a shallow copy of r with URL.Path replaced —
// http.FileServer serves whatever path the request carries, so falling
// back to index.html means handing it a request whose path actually says
// "/", not mutating the caller's own *http.Request in place.
func cloneWithPath(r *http.Request, path string) *http.Request {
	clone := r.Clone(r.Context())
	clone.URL.Path = path
	return clone
}
