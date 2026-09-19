// Package httpapi is the shared HTTP layer every product module builds its
// public /api/v1 routes on (RFC-001 §5). It owns the request-scoped
// concerns the RFC requires of every response — a fresh X-Request-ID,
// Cache-Control: no-store, the closed error envelope, the 64 KB JSON body
// limit, and the Origin check on mutating requests (ADR-008) — so that no
// module has to reimplement them.
//
// It does not know about any module's routes: NewMux returns a bare
// http.ServeMux whose unmatched-path fallback answers with the same error
// envelope, and modules register their own "METHOD /api/v1/..." patterns
// on it (cmd/emsim/api.go composes the process from there).
package httpapi
