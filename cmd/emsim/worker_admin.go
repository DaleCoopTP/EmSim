// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// cmd/worker/admin.go; adapted: role is tasks.Role instead of worker.Role,
// and InstrumentHTTP takes observability.AdminRouteNamer explicitly (see
// internal/platform/observability/http.go).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"emsim/internal/platform/admin"
	"emsim/internal/platform/config"
	"emsim/internal/platform/observability"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/tasks"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

type workerTelemetry struct {
	registry *prometheus.Registry
	metrics  *observability.Metrics
	logger   observability.Logger
}

func newWorkerTelemetry(role tasks.Role, logLevel string) (workerTelemetry, error) {
	registry := prometheus.NewRegistry()
	metrics, err := observability.NewMetrics(registry, string(role))
	if err != nil {
		return workerTelemetry{}, errors.New("observability configuration is invalid")
	}
	logger := observability.NewLogger(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: config.SlogLevel(logLevel)})), "worker", string(role))
	return workerTelemetry{registry: registry, metrics: metrics, logger: logger}, nil
}

type workerAdmin struct {
	server     *http.Server
	processCtx context.Context
	pool       *pgxpool.Pool
	metrics    *observability.Metrics
	role       string
	live       atomic.Bool
}

func newWorkerAdmin(ctx context.Context, cfg config.Worker, pool *pgxpool.Pool, telemetry workerTelemetry) *workerAdmin {
	workerAdmin := &workerAdmin{processCtx: ctx, pool: pool, metrics: telemetry.metrics, role: string(cfg.Role)}
	workerAdmin.live.Store(true)
	workerAdmin.metrics.SetReady(workerAdmin.role, true)
	handler := observability.InstrumentHTTP(
		admin.AdminWithMetrics(workerAdmin.ready, telemetry.registry), telemetry.metrics, telemetry.logger, observability.AdminRouteNamer,
	)
	workerAdmin.server = &http.Server{Addr: cfg.AdminAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	return workerAdmin
}

func (a *workerAdmin) stop() {
	a.live.Store(false)
	a.metrics.SetReady(a.role, false)
}

func (a *workerAdmin) ready(ctx context.Context) bool {
	ready := a.databaseReady(ctx)
	a.metrics.SetReady(a.role, ready)
	return ready
}

func (a *workerAdmin) databaseReady(ctx context.Context) bool {
	if !a.live.Load() || a.processCtx.Err() != nil || ctx.Err() != nil {
		return false
	}
	if err := pgstore.Ping(ctx, a.pool); err != nil {
		return false
	}
	ready, err := pgstore.Ready(ctx, a.pool)
	return err == nil && ready
}
