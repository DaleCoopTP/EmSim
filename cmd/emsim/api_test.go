// New tests: core's cmd/api had no main_test.go. serveAPI/shutdownAPIServers
// mirror cmd/worker/runtime.go's serve pattern (ported into worker.go as
// serveWorker) but for two listeners instead of one server + one
// supervisor, so it gets its own coverage here.
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeAPIReturnsErrorWhenAServerCannotListen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	good := &http.Server{Addr: "127.0.0.1:0", Handler: publicNotFoundMux()}
	bad := &http.Server{Addr: "invalid-address", Handler: publicNotFoundMux()}
	if err := serveAPI(ctx, good, bad); err == nil {
		t.Fatal("listen failure on one server was accepted")
	}
}

func TestServeAPIShutsDownCleanlyOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := &http.Server{Addr: "127.0.0.1:0", Handler: publicNotFoundMux()}
	done := make(chan error, 1)
	go func() { done <- serveAPI(ctx, server) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serveAPI() error = %v", err)
	}
}

func TestPublicNotFoundMuxServes404WithNoStore(t *testing.T) {
	response := httptest.NewRecorder()
	publicNotFoundMux().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/anything", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}
