package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func passThroughHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireSameOriginAllowsSafeMethodsWithoutOriginCheck(t *testing.T) {
	handler := RequireSameOrigin(passThroughHandler())
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		r := httptest.NewRequest(method, "https://emsim.local/api/v1/scenarios", nil)
		r.Header.Set("Origin", "https://attacker.example")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		if response.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 (safe methods are never origin-checked)", method, response.Code)
		}
	}
}

func TestRequireSameOriginAllowsMatchingOrigin(t *testing.T) {
	handler := RequireSameOrigin(passThroughHandler())
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.Host = "emsim.local"
	r.Header.Set("Origin", "https://emsim.local")

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
}

func TestRequireSameOriginRejectsCrossOrigin(t *testing.T) {
	handler := RequireSameOrigin(WithRequestID(passThroughHandler()))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.Host = "emsim.local"
	r.Header.Set("Origin", "https://attacker.example")

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestRequireSameOriginAllowsMissingOriginHeader(t *testing.T) {
	handler := RequireSameOrigin(passThroughHandler())
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.Host = "emsim.local"

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no Origin header is allowed through)", response.Code)
	}
}

func TestRequireSameOriginIgnoresSchemeMismatch(t *testing.T) {
	// The Go server sees plain HTTP behind the reverse proxy even when the
	// browser's Origin is https (ADR: Caddy terminates TLS) — only Host is
	// compared, so an Origin whose scheme differs from what this request
	// looks like at the Go layer must still be allowed.
	handler := RequireSameOrigin(passThroughHandler())
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.Host = "emsim.local"
	r.Header.Set("Origin", "http://emsim.local")

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
}
