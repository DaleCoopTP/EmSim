// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// cmd/api/main.go; adapted: no httpapi.API/runapp.Service — core's public
// server served exactly one route (POST/GET /v1/runs); this skeleton's
// public server has none of its own yet (auth/content/training/
// assessment/reporting each add their routes on top of this in later
// commits). It now serves the shared public-API middleware chain from
// internal/platform/httpapi (request id, no-store, Origin check, JSON 404
// envelope) instead of a bare 404 handler, instrumented the same way the
// real routes will be. ready() drops the profile registry check (no
// profiles were ported, see docs/technical-discovery.md §3.5).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"emsim/internal/platform/admin"
	"emsim/internal/platform/config"
	"emsim/internal/platform/httpapi"
	"emsim/internal/platform/observability"
	pgstore "emsim/internal/platform/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

const apiShutdownTimeout = 10 * time.Second

func runAPI(ctx context.Context, args []string) error {
	if len(args) != 0 {
		return errAPITakesNoArgs
	}
	processConfig, err := config.APIFromEnvironment(os.Getenv)
	if err != nil {
		return err
	}
	pool, err := pgstore.Open(ctx, processConfig.DatabaseURL)
	if err != nil {
		return errors.New("database connection is unavailable")
	}
	defer pool.Close()
	if !apiSchemaReady(ctx, pool) {
		return errors.New("database schema is not ready")
	}

	metricRegistry := prometheus.NewRegistry()
	metrics, err := observability.NewMetrics(metricRegistry, "api")
	if err != nil {
		return errors.New("observability configuration is invalid")
	}
	logger := observability.NewLogger(slog.New(slog.NewJSONHandler(os.Stderr, nil)), "api", "api")
	var live atomic.Bool
	live.Store(true)
	defer live.Store(false)
	readiness := func(requestCtx context.Context) bool {
		isReady := live.Load() && ctx.Err() == nil && requestCtx.Err() == nil && apiSchemaReady(requestCtx, pool)
		metrics.SetReady("api", isReady)
		return isReady
	}

	adminHandler := observability.InstrumentHTTP(
		admin.AdminWithMetrics(readiness, metricRegistry), metrics, logger, observability.AdminRouteNamer,
	)
	publicHandler := observability.InstrumentHTTP(newPublicHandler(), metrics, logger, observability.PatternRouteNamer)
	publicServer := &http.Server{Addr: processConfig.PublicAddr, Handler: publicHandler, ReadHeaderTimeout: 5 * time.Second}
	adminServer := &http.Server{Addr: processConfig.AdminAddr, Handler: adminHandler, ReadHeaderTimeout: 5 * time.Second}

	metrics.SetReady("api", true)
	defer metrics.SetReady("api", false)
	return serveAPI(ctx, publicServer, adminServer)
}

var errAPITakesNoArgs = errors.New("api subcommand takes no arguments")

// newPublicHandler is the public server until a module registers its first
// route on the mux; it exists so InstrumentHTTP has something real to
// wrap and the listener comes up in every environment, including one with
// no domain modules yet. A module adding real endpoints (auth first, see
// slice 1) registers "METHOD /api/v1/..." patterns on this same mux
// instead of replacing this function.
func newPublicHandler() http.Handler {
	return httpapi.WrapPublic(httpapi.NewMux())
}

func apiSchemaReady(ctx context.Context, pool *pgxpool.Pool) bool {
	if err := pgstore.Ping(ctx, pool); err != nil {
		return false
	}
	ready, err := pgstore.Ready(ctx, pool)
	return err == nil && ready
}

func serveAPI(ctx context.Context, servers ...*http.Server) error {
	errorsCh := make(chan error, len(servers))
	for _, server := range servers {
		server := server
		go func() { errorsCh <- server.ListenAndServe() }()
	}
	received := 0
	var listenErr error
	select {
	case <-ctx.Done():
	case err := <-errorsCh:
		received++
		if !errors.Is(err, http.ErrServerClosed) {
			listenErr = err
		}
	}
	shutdownAPIServers(servers)
	for received < len(servers) {
		<-errorsCh
		received++
	}
	if listenErr != nil {
		return fmt.Errorf("listen: %w", listenErr)
	}
	return nil
}

func shutdownAPIServers(servers []*http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), apiShutdownTimeout)
	defer cancel()
	var group sync.WaitGroup
	for _, server := range servers {
		group.Add(1)
		go func() {
			defer group.Done()
			_ = server.Shutdown(ctx)
		}()
	}
	group.Wait()
}
