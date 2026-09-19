package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestAdminEndpoints(t *testing.T) {
	for _, test := range []struct {
		path       string
		ready      bool
		wantStatus int
	}{
		{path: "/healthz", wantStatus: http.StatusOK},
		{path: "/readyz", ready: true, wantStatus: http.StatusOK},
		{path: "/readyz", ready: false, wantStatus: http.StatusServiceUnavailable},
	} {
		response := httptest.NewRecorder()
		Admin(func(context.Context) bool { return test.ready }).ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != test.wantStatus {
			t.Fatalf("%s ready=%t status = %d", test.path, test.ready, response.Code)
		}
	}
}

func TestAdminMetricsUseOnlyInjectedRegistry(t *testing.T) {
	registry := prometheus.NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "injected_only_total", Help: "Test metric."})
	registry.MustRegister(counter)
	counter.Inc()
	calls := 0
	response := httptest.NewRecorder()
	AdminWithMetrics(func(context.Context) bool { calls++; return true }, registry).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK || calls != 1 || !strings.Contains(response.Body.String(), "injected_only_total") || strings.Contains(response.Body.String(), "go_goroutines") {
		t.Fatalf("metrics response = %d %s", response.Code, response.Body.String())
	}
}
