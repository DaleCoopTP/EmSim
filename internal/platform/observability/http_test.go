// New tests: RouteNamer and AdminRouteNamer are new in this port — core's
// InstrumentHTTP hard-coded its one route (see http.go).
package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestAdminRouteNamerMapsKnownAndUnknownPaths(t *testing.T) {
	cases := map[string]string{
		"/healthz": "health", "/readyz": "ready", "/metrics": "metrics", "/v1/scenarios/123": "unknown",
	}
	for path, want := range cases {
		if got := AdminRouteNamer(path); got != want {
			t.Fatalf("AdminRouteNamer(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestInstrumentHTTPUsesInjectedRouteNamer(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := NewMetrics(registry, "api")
	if err != nil {
		t.Fatal(err)
	}
	logger := NewLogger(nil, "api", "api")
	handler := InstrumentHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), metrics, logger, AdminRouteNamer)

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
