// New tests: core's cmd/api had no main_test.go. serveAPI/shutdownAPIServers
// mirror cmd/worker/runtime.go's serve pattern (ported into worker.go as
// serveWorker) but for two listeners instead of one server + one
// supervisor, so it gets its own coverage here.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"emsim/internal/platform/config"
	"emsim/internal/platform/httpapi"
	"emsim/internal/platform/realtime"
)

// testPublicHandler builds newPublicHTTP with a nil pool and a
// zero-value config: every test in this file only exercises routing/
// middleware behavior (listen failures, shutdown, the 404 fallback),
// never a handler that would actually query the database.
func testPublicHandler() http.Handler {
	handler, _, _ := newPublicHTTP(nil, config.API{}, realtime.NewHub())
	return handler
}

func TestServeAPIReturnsErrorWhenAServerCannotListen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	good := &http.Server{Addr: "127.0.0.1:0", Handler: testPublicHandler()}
	bad := &http.Server{Addr: "invalid-address", Handler: testPublicHandler()}
	if err := serveAPI(ctx, good, bad); err == nil {
		t.Fatal("listen failure on one server was accepted")
	}
}

func TestServeAPIShutsDownCleanlyOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := &http.Server{Addr: "127.0.0.1:0", Handler: testPublicHandler()}
	done := make(chan error, 1)
	go func() { done <- serveAPI(ctx, server) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serveAPI() error = %v", err)
	}
}

// The path is under /api/ (apiMux's own fallback), not a bare "/anything"
// — a path outside /api/ goes to the SPA handler instead (static.go),
// whose own response depends on whether web/dist has actually been built
// (web/embed.go embeds whatever is on disk at compile time), which this
// test must not depend on. static_test.go covers the SPA handler's own
// not-built/fallback behavior deterministically via a fake fs.FS.
func TestPublicHandlerServesJSONNotFoundWithNoStoreAndRequestID(t *testing.T) {
	response := httptest.NewRecorder()
	testPublicHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get(httpapi.RequestIDHeader) == "" {
		t.Fatal("X-Request-ID header missing")
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "not_found" {
		t.Fatalf("error.code = %q, want not_found", body.Error.Code)
	}
	if body.RequestID != response.Header().Get(httpapi.RequestIDHeader) {
		t.Fatalf("body request_id = %q, header = %q", body.RequestID, response.Header().Get(httpapi.RequestIDHeader))
	}
}

// TestPublicHandlerRoutesAPIPrefixToTheAPIMux proves "/api/" reaches the
// real auth routes rather than the SPA handler mounted at "/" (static.go)
// — logout is the one auth endpoint safe to exercise against a nil pool:
// with no session cookie on the request it never touches the database.
func TestPublicHandlerRoutesAPIPrefixToTheAPIMux(t *testing.T) {
	response := httptest.NewRecorder()
	testPublicHandler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (proves /api/v1/auth/logout reached the real handler, not the SPA fallback)", response.Code)
	}
}
