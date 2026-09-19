package httpapi

import (
	"net/http"
	"net/url"
)

// RequireSameOrigin rejects a state-changing request whose Origin header
// names a different host than the one serving this API (ADR-008: "CSRF
// закрыт SameSite=Strict + проверкой Origin на мутациях"). GET/HEAD/
// OPTIONS pass through unchecked — they must stay side-effect free by
// convention.
//
// Only the Host is compared, not the scheme: behind the reverse proxy the
// RFC puts in front of the API (Caddy, terminating TLS), the Go server
// itself sees plain HTTP even though the browser's Origin is https, and
// nothing here can tell a trusted proxy hop from a spoofed one. Host
// already carries the meaningful cross-origin signal — a page on another
// domain has a different Host — and SameSite=Strict is the primary CSRF
// control; this check is defense in depth, not the only one.
//
// A request with no Origin header at all is let through: same-origin
// fetches always send one in modern browsers, and rejecting a header the
// RFC does not otherwise require would only break non-browser API
// clients.
func RequireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !sameHost(origin, r.Host) {
			WriteError(w, r, CodeForbidden, "cross-origin request rejected", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func sameHost(origin, host string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Host == host
}
