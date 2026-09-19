package httpapi

import "net/http"

// NewMux returns an http.ServeMux whose unmatched-path fallback answers
// with NotFoundJSON instead of the stdlib mux's empty 404 body. Modules
// register their own "METHOD /api/v1/..." patterns on the returned mux
// before it serves traffic; a request whose path matches a registered
// pattern but not its method still gets the stdlib mux's automatic 405
// (plain text, not this package's envelope) — that only starts happening
// once a module actually registers routes.
func NewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", NotFoundJSON)
	return mux
}

// WrapPublic applies the middleware every public API request goes
// through, in the order the RFC requires them: a request id and
// Cache-Control: no-store first (WithRequestID), then the Origin check on
// mutations (RequireSameOrigin) — in that order so a rejected request's
// error envelope still carries a request id.
func WrapPublic(mux *http.ServeMux) http.Handler {
	return WithRequestID(RequireSameOrigin(mux))
}
