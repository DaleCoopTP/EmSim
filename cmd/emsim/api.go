// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// cmd/api/main.go; adapted: no httpapi.API/runapp.Service — core's public
// server served exactly one route (POST/GET /v1/runs). This process now
// composes auth (slice 1: login/logout/me) and content (slice 2: the
// instructor catalogue — GET /services, /scenarios*) on top of the shared
// public-API middleware chain from internal/platform/httpapi (request id,
// no-store, Origin check, JSON 404 envelope); training/assessment/
// reporting register their own routes on the same mux in later slices.
// It also serves the embedded SPA build (static.go, web/embed.go)
// for everything outside "/api/" (ADR-009). ready() drops the profile
// registry check (no profiles were ported, see docs/technical-discovery.md
// §3.5).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	assessmenthttp "emsim/internal/assessment/http"
	"emsim/internal/auth"
	authhttp "emsim/internal/auth/http"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contenthttp "emsim/internal/content/http"
	contentpg "emsim/internal/content/postgres"
	"emsim/internal/content/schema"
	"emsim/internal/platform/admin"
	"emsim/internal/platform/config"
	"emsim/internal/platform/httpapi"
	"emsim/internal/platform/maintenance"
	"emsim/internal/platform/observability"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/platform/realtime"
	"emsim/internal/platform/status"
	"emsim/internal/platform/tasks"
	"emsim/internal/reporting"
	reportinghttp "emsim/internal/reporting/http"
	reportingpg "emsim/internal/reporting/postgres"
	"emsim/internal/training"
	traininghttp "emsim/internal/training/http"
	trainingpg "emsim/internal/training/postgres"
	"emsim/web"

	"github.com/google/uuid"
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
	logger := observability.NewLogger(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: config.SlogLevel(processConfig.LogLevel)})), "api", "api")
	var live atomic.Bool
	live.Store(true)
	defer live.Store(false)
	readiness := func(requestCtx context.Context) bool {
		isReady := live.Load() && ctx.Err() == nil && requestCtx.Err() == nil && apiSchemaReady(requestCtx, pool)
		metrics.SetReady("api", isReady)
		return isReady
	}

	// ADR-018: LISTEN is established before recovery, so nothing NOTIFYed
	// while recovery runs can be missed for want of a subscription that
	// was not there yet. RunListener retries forever on its own; here we
	// only wait for its very first successful LISTEN (or ctx/timeout).
	hub := realtime.NewHub()
	listenerReady := make(chan struct{})
	backgroundCtx, stopBackground := context.WithCancel(ctx)
	defer stopBackground()
	go realtime.RunListener(backgroundCtx, pool, hub, logger, listenerReady)
	select {
	case <-listenerReady:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(realtimeListenTimeout):
		return errors.New("realtime listener did not start")
	}

	adminHandler := observability.InstrumentHTTP(
		admin.AdminWithMetrics(readiness, metricRegistry), metrics, logger, observability.AdminRouteNamer,
	)
	// ADR-038: the status screen's load panel reads this rolling window.
	window := observability.NewWindow(nil)
	publicRoutes, publicRouteName, trainingService := newPublicHTTP(pool, processConfig, hub, window)
	publicHandler := observability.InstrumentHTTP(publicRoutes, metrics, logger, publicRouteName, observability.WithWindow(window))
	publicServer := &http.Server{Addr: processConfig.PublicAddr, Handler: publicHandler, ReadHeaderTimeout: 5 * time.Second}
	adminServer := &http.Server{Addr: processConfig.AdminAddr, Handler: adminHandler, ReadHeaderTimeout: 5 * time.Second}

	// RFC-001 §7.2's restart recovery runs once, synchronously, before
	// this process ever reports ready: every item left open by a prior
	// process gets one idempotent interruption marker, with no request
	// accepted (readiness still false) in the meantime.
	recoveryID := uuid.New()
	affected, err := trainingService.Recover(ctx, recoveryID, trainingRecoveryCause)
	if err != nil {
		return fmt.Errorf("training recovery: %w", err)
	}
	recoverOutcome := "clean"
	if len(affected) > 0 {
		recoverOutcome = "interrupted"
	}
	logger.Operation(ctx, slog.LevelInfo, "training_recover", recoverOutcome, "", "")

	metrics.SetReady("api", true)
	defer metrics.SetReady("api", false)

	go runTrainingScheduler(backgroundCtx, trainingService, logger)
	go runAPIProber(backgroundCtx, pool, processConfig)

	return serveAPI(ctx, publicServer, adminServer)
}

// realtimeListenTimeout bounds how long runAPI waits for the first
// LISTEN before giving up and failing startup outright — a PostgreSQL
// that never accepts a LISTEN this long is not a condition retrying
// forever inside this one call would ever recover from; apiSchemaReady
// already gated on Ping/Ready before this point, so a plain connection
// problem is not the expected cause.
const realtimeListenTimeout = 15 * time.Second

const trainingRecoveryCause = "server_restart"

// activeSessionWindow is what "active" means on the load panel: the
// session made an authenticated request within this long (ADR-038).
const activeSessionWindow = 5 * time.Minute

// runTrainingScheduler is the durable time-based training work loop
// (RFC-001 §7.2's scheduler tick — due scenario events and hard-level
// offers, internal/training.Service.Tick). It ticks every 500 ms until
// ctx is cancelled; a single failed Tick is logged and retried on the
// next tick rather than treated as fatal (transient DB contention is
// expected and Tick is designed to be safely re-run). RFC-001's "a
// scheduler crash makes the process unhealthy and leads to a restart" is
// satisfied by ordinary Go semantics: an unrecovered panic here brings
// down the whole binary, which docker's restart policy then restarts —
// nothing here should catch and swallow one.
func runTrainingScheduler(ctx context.Context, svc *training.Service, logger observability.Logger) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := svc.Tick(ctx); err != nil && ctx.Err() == nil {
				logger.Operation(ctx, slog.LevelError, "training_scheduler_tick", "error", "", "tick_failed")
			}
		}
	}
}

var errAPITakesNoArgs = errors.New("api subcommand takes no arguments")

// newPublicHTTP also returns the composed *training.Service so runAPI can
// drive it outside the HTTP path: the C6 restart-recovery marker (before
// readiness) and the C5/C6 scheduler tick loop (500 ms, RFC-001 §7.2)
// both need the same Service instance the HTTP handlers use, not a
// second one built from the same pool. hub is runAPI's own realtime.Hub
// (C9) — SSE handlers read from it, training's domain code publishes to
// it via realtime.NotifyTx inside its own transactions.
func newPublicHTTP(pool *pgxpool.Pool, cfg config.API, hub *realtime.Hub, window *observability.Window) (http.Handler, observability.RouteNamer, *training.Service) {
	apiMux := httpapi.NewMux()

	// contentService is built first: auth.NewService takes it as its
	// ServiceCatalog port (service_code validation, slice-2-plan.md's
	// C5) — content has no dependency on auth, so this order is the only
	// one that avoids a forward reference.
	contentService := content.NewService(contentpg.NewStore(pool), mustSchemaValidator())

	authStore := authpg.NewStore(pool)
	authService := auth.NewService(authStore, auth.NewPasswordIdentityProvider(authStore), cfg.SessionTTL, nil, contentService)
	authhttp.NewHandlers(authService, cfg.CookieSecure).Register(apiMux)
	authhttp.NewAdminHandlers(authService, cfg.CookieSecure).Register(apiMux)

	// trainingService is built before content's own handlers (112-7/
	// ADR-027: POST /scenarios/{id}/preview-runs needs training.Service.
	// StartPreview as its previewStarter port) — content itself still
	// never imports training, only content/http does, the same way
	// assessment/reporting's own http packages already take
	// *training.Service.
	// ADR-029: the api enqueues the AI caller's prompt-cache warm-ups and
	// holds back the opening; the worker runs the warm-ups.
	trainingService := newTrainingService(pool, mustTaskEnqueuer(pool), cfg.AssessmentJudge == config.AssessmentJudgeLLM).
		WithCallerTiming(training.CallerTiming{Warmup: cfg.CallerWarmup, OpeningDelay: cfg.CallerOpeningDelay}).
		WithMaintenance(maintenance.Gate{}).
		WithDictation(newTranscriber(cfg.Dictation), dictationSettings(cfg.Dictation))
	contenthttp.NewHandlers(contentService, trainingService, authService, cfg.CookieSecure).Register(apiMux)
	traininghttp.NewHandlers(trainingService, authService, cfg.CookieSecure, hub).Register(apiMux)
	assessmentService := newAssessmentService(pool, mustTaskEnqueuer(pool), nil)
	assessmenthttp.NewHandlers(assessmentService, trainingService, authService, cfg.CookieSecure).Register(apiMux)
	reportingService := reporting.NewService(reportingpg.NewStore(pool), mustTaskEnqueuer(pool))
	reportinghttp.NewHandlers(reportingService, trainingService, authService, cfg.CookieSecure).Register(apiMux)
	// ADR-033: the administrator's status screen and manual backup. The
	// status package is platform code and knows nothing of sessions; the
	// admin-only guard and the actor come from the auth module here.
	adminOnly := func(handler http.HandlerFunc) http.Handler {
		return authhttp.SessionMiddleware(authService, cfg.CookieSecure)(authhttp.RequireRole(auth.GroupAdmin)(handler))
	}
	anyRole := func(handler http.HandlerFunc) http.Handler {
		return authhttp.SessionMiddleware(authService, cfg.CookieSecure)(authhttp.RequireRole(auth.GroupAuth)(handler))
	}
	sessionActor := func(ctx context.Context) (uuid.UUID, string, bool) {
		principal, ok := authhttp.PrincipalFromContext(ctx)
		return principal.UserID, string(principal.Role), ok
	}
	// Admin retry guards (ADR-033): an assessment is never evaluated twice
	// and a caller turn's reply or warm-up has no meaning later.
	retryGuards := map[tasks.Kind]tasks.RetryGuard{
		training.KindAssessmentEvaluate: assessmentService,
		training.KindCallerReply:        tasks.NeverRetry{},
		training.KindCallerWarmup:       tasks.NeverRetry{},
	}
	statusHandlers := status.NewHandlers(pool, mustTaskEnqueuer(pool), retryGuards, kindBackupRun, os.Getenv("BLOB_ROOT"), pgstore.ExpectedSchemaVersion, sessionActor).
		WithLogins(authStore.LoginsByID).
		WithBuild(buildVersion).
		WithConfig(cfg.Public(os.Getenv("BLOB_ROOT"))).
		WithLoad(status.LoadSources{
			Window:  window,
			Streams: hub.Streams,
			ActiveSessions: func(ctx context.Context) (int, error) {
				return authStore.ActiveSessions(ctx, activeSessionWindow)
			},
			Activity: func(ctx context.Context) (status.Activity, error) {
				lessons, items, err := trainingpg.NewStore(pool).ActivityCounts(ctx)
				return status.Activity{RunningLessons: lessons, OpenItems: items}, err
			},
		})
	statusHandlers.Register(apiMux, adminOnly)
	statusHandlers.RegisterSystem(apiMux, anyRole)

	root := http.NewServeMux()
	root.Handle("/api/", apiMux)
	root.Handle("/", newSPAHandler(web.DistFS))

	apiRouteName := observability.PatternRouteNamer(apiMux)
	routeName := func(r *http.Request) string {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			return apiRouteName(r)
		}
		return "spa"
	}
	return httpapi.WrapPublic(root), routeName, trainingService
}

// mustSchemaValidator compiles the embedded scenario/scenario-file JSON
// Schemas once per process. A failure here can only mean the embedded
// contracts themselves are malformed — a build-time invariant, not a
// runtime condition to recover from (internal/auth/password.go's
// mustHashDummy is the same pattern for its own always-succeeds-in-
// practice precomputation).
func mustSchemaValidator() *schema.Validator {
	validator, err := schema.New()
	if err != nil {
		panic("emsim: failed to compile embedded scenario schemas: " + err.Error())
	}
	return validator
}

// mustTaskEnqueuer builds the api process's own *tasks.Store purely to
// enqueue (training.Stop's KindLessonClose) — it never claims, and no
// worker pool runs in this process. Its Registry is built by the exact
// same registerKinds the worker calls (worker_composition.go), so the
// Spec (priority/max_attempts) EnqueueTx reads is guaranteed identical
// to what the worker will later run the task under. A failure here can
// only mean a malformed Spec constant — a build-time invariant, the same
// class of always-succeeds-in-practice precomputation as
// mustSchemaValidator above.
func mustTaskEnqueuer(pool *pgxpool.Pool) *tasks.Store {
	registry, err := tasks.NewRegistry(tasks.DefaultPolicy())
	if err != nil {
		panic("emsim: invalid task queue policy: " + err.Error())
	}
	if err := registerKinds(registry); err != nil {
		panic("emsim: invalid task kind registration: " + err.Error())
	}
	return tasks.NewStore(pool, registry)
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

// runAPIProber records what only the api can observe for the status screen
// (ADR-038): whether the speech engine answers, when dictation runs on
// whisper. The worker probes the language model the same way.
func runAPIProber(ctx context.Context, pool *pgxpool.Pool, cfg config.API) {
	dictation := cfg.Dictation
	if dictation.Engine != config.DictationWhisper || dictation.STTURL == "" {
		return
	}
	probes := []status.Probe{{
		Component: status.ComponentSTT,
		Check:     status.HTTPCheck(strings.TrimSuffix(dictation.STTURL, "/")+"/health", "", map[string]any{"model": dictation.Model}),
	}}
	prober := status.NewProber(probes, status.NewStore(pool), 30*time.Second, 5*time.Second,
		func(interval time.Duration) status.Ticker { return tasks.SystemTickerFactory{}.NewTicker(interval) })
	_ = prober.Run(ctx)
}
