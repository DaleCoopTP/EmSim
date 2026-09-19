// New tests: RouteNamer and AdminRouteNamer are new in this port — core's
// InstrumentHTTP hard-coded its one route (see http.go). RouteNamer reads
// r.Pattern, which only an http.ServeMux sets, so these tests route
// requests through a real mux rather than calling the namer on a bare path
// or handing a bare HandlerFunc to InstrumentHTTP.
package observability

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func adminTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, pattern := range []string{"/healthz", "/readyz", "/metrics"} {
		mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	}
	return mux
}

func TestAdminRouteNamerMapsKnownAndUnknownPaths(t *testing.T) {
	mux := adminTestMux()
	cases := map[string]string{
		"/healthz": "health", "/readyz": "ready", "/metrics": "metrics", "/v1/scenarios/123": "unknown",
	}
	for path, want := range cases {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		mux.ServeHTTP(httptest.NewRecorder(), r)
		if got := AdminRouteNamer(r); got != want {
			t.Fatalf("AdminRouteNamer(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestPatternRouteNamerReportsMatchedPatternOrUnknown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(http.ResponseWriter, *http.Request) {})
	routeName := PatternRouteNamer(mux)

	matched := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	if got := routeName(matched); got != "api_v1_auth_login" {
		t.Fatalf("PatternRouteNamer(matched) = %q, want the registered pattern", got)
	}

	unmatched := httptest.NewRequest(http.MethodGet, "/nope", nil)
	if got := routeName(unmatched); got != "unknown" {
		t.Fatalf("PatternRouteNamer(unmatched) = %q, want unknown", got)
	}
}

func TestInstrumentHTTPNamesRouteFromNestedMux(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry, "api")
	if err != nil {
		t.Fatal(err)
	}
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/v1/me", func(http.ResponseWriter, *http.Request) {})
	root := http.NewServeMux()
	root.Handle("/api/", apiMux)
	handler := InstrumentHTTP(root, metrics, NewLogger(nil, "api", "api"), PatternRouteNamer(apiMux))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if text := exposition(t, registry); !strings.Contains(text, `route="api_v1_me"`) {
		t.Fatalf("nested route collapsed to unknown: %s", text)
	}
}

func TestInstrumentHTTPSuppressesRoutineProbeLogs(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry, "api")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger := NewLogger(slog.New(slog.NewJSONHandler(&output, nil)), "api", "api")
	handler := InstrumentHTTP(adminTestMux(), metrics, logger, AdminRouteNamer)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if output.Len() != 0 {
		t.Fatalf("routine readiness probe was logged: %s", output.String())
	}

	failingProbe := InstrumentHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}), metrics, logger, AdminRouteNamer)
	failingProbe.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if output.Len() == 0 {
		t.Fatal("failed readiness probe was not logged")
	}
}

func TestInstrumentHTTPUsesInjectedRouteNamer(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry, "api")
	if err != nil {
		t.Fatal(err)
	}
	logger := NewLogger(nil, "api", "api")
	mux := adminTestMux()
	handler := InstrumentHTTP(mux, metrics, logger, AdminRouteNamer)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	text := exposition(t, registry)
	if !strings.Contains(text, `route="health"`) {
		t.Fatalf("missing route label in %s", text)
	}
}
