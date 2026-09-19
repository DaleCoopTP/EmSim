package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithRequestIDSetsHeaderNoStoreAndContext(t *testing.T) {
	var sawInContext string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawInContext = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	response := httptest.NewRecorder()
	WithRequestID(next).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	headerID := response.Header().Get(RequestIDHeader)
	if headerID == "" {
		t.Fatal("X-Request-ID header not set")
	}
	if sawInContext != headerID {
		t.Fatalf("RequestIDFromContext() = %q, want header value %q", sawInContext, headerID)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestWithRequestIDIgnoresClientSuppliedHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, "client-supplied-value")
	WithRequestID(next).ServeHTTP(response, request)

	if got := response.Header().Get(RequestIDHeader); got == "client-supplied-value" {
		t.Fatalf("X-Request-ID = %q, want a server-generated value, not the client's", got)
	}
}

func TestWithRequestIDGeneratesDistinctIDsPerRequest(t *testing.T) {
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	handler := WithRequestID(next)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))

	if first.Header().Get(RequestIDHeader) == second.Header().Get(RequestIDHeader) {
		t.Fatal("two requests got the same X-Request-ID")
	}
}

func TestRequestIDFromContextWithoutMiddlewareReturnsEmpty(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := RequestIDFromContext(request.Context()); got != "" {
		t.Fatalf("RequestIDFromContext() = %q, want empty", got)
	}
}
