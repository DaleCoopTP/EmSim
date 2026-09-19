package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewMuxServesJSONNotFoundForUnmatchedPath(t *testing.T) {
	mux := NewMux()
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/anything", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != string(CodeNotFound) {
		t.Fatalf("error.code = %q, want not_found", body.Error.Code)
	}
}

func TestNewMuxServesRegisteredRoute(t *testing.T) {
	mux := NewMux()
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
}

func TestWrapPublicChainsRequestIDAndOriginCheck(t *testing.T) {
	mux := NewMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := WrapPublic(mux)

	// Cross-origin mutation: rejected, but still carries a request id.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.Host = "emsim.local"
	r.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	if response.Header().Get(RequestIDHeader) == "" {
		t.Fatal("rejected response is missing X-Request-ID")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}

	var body struct {
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.RequestID == "" || body.RequestID != response.Header().Get(RequestIDHeader) {
		t.Fatalf("body request_id = %q, header = %q", body.RequestID, response.Header().Get(RequestIDHeader))
	}
}

func TestWrapPublicAllowsSameOriginMutation(t *testing.T) {
	mux := NewMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := WrapPublic(mux)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	r.Host = "emsim.local"
	r.Header.Set("Origin", "https://emsim.local")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
}
