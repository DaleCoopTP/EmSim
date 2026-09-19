package http

import (
	"context"
	"net/http"

	"emsim/internal/auth"
	"emsim/internal/platform/httpapi"
)

type principalKeyType struct{}

var principalKey principalKeyType

// authenticator is the subset of *auth.Service SessionMiddleware needs —
// declared here (its consumer) rather than depending on *auth.Service
// directly, so a fake service can drive tests without a real Store behind
// it.
type authenticator interface {
	Authenticate(ctx context.Context, token string) (auth.Principal, error)
}

// SessionMiddleware authenticates every request via the session cookie
// and, on success, stores the resulting auth.Principal in the request
// context for downstream handlers and RequireRole. A missing or invalid
// session answers 401 directly, so a handler behind this middleware never
// has to check for a missing Principal itself — only PrincipalFromContext,
// which is safe to treat as always-present there.
func SessionMiddleware(service authenticator, cookieSecure bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := sessionTokenFromRequest(r)
			if token == "" {
				httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "authentication required", nil)
				return
			}
			principal, err := service.Authenticate(r.Context(), token)
			if err != nil {
				httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "authentication required", nil)
				return
			}
			// Authenticate may have extended the server-side sliding expiry.
			// Mirror the authoritative expiry into the browser cookie; otherwise
			// the browser would discard a still-valid renewed session at the
			// original login deadline.
			if !principal.SessionExpiresAt.IsZero() {
				SetSessionCookie(w, token, principal.SessionExpiresAt, cookieSecure)
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, principal)))
		})
	}
}

// PrincipalFromContext returns the Principal SessionMiddleware stored, or
// the zero value and false if this request never went through it.
func PrincipalFromContext(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(principalKey).(auth.Principal)
	return p, ok
}

// RequireRole wraps next so only a request whose Principal is Allowed for
// group may reach it (auth.Allowed, authz.go — the single role table
// every route group checks against). It must sit behind SessionMiddleware:
// a request with no Principal in context is rejected the same as a
// disallowed one, both as 401/403 respectively, never a panic.
func RequireRole(group auth.Group) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFromContext(r.Context())
			if !ok {
				httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "authentication required", nil)
				return
			}
			if !auth.Allowed(principal.Role, group) {
				httpapi.WriteError(w, r, httpapi.CodeForbidden, "insufficient role", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
