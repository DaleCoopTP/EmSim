package http

import (
	"net/http"
	"time"
)

// CookieName is the session cookie's name (ADR-008).
const CookieName = "emsim_session"

// SetSessionCookie sets the session cookie per ADR-008: HttpOnly,
// SameSite=Strict, Path=/, and Secure when secure is true. secure comes
// from internal/platform/config.API.CookieSecure — true by default (RFC-001
// puts TLS termination at Caddy in front of this process), false in
// compose's demo profile, which has no Caddy and would otherwise never see
// the cookie echoed back over plain HTTP.
func SetSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// ClearSessionCookie expires the cookie immediately after Logout has either
// revoked the session or established that it was already absent. Storage
// failures do not clear it, so the client cannot mistake a failed revocation
// for success.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// sessionTokenFromRequest reads the cookie's raw value, or "" if the
// request carries none.
func sessionTokenFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}
