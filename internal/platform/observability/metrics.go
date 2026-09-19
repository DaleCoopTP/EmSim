// internal/observability/metrics.go; adapted:
//   - namespace "orchestration_core" -> "emsim".
//   - Runner (internal/platform/tasks) is now identified by pool name
//     (e.g. "short"/"llm"/"stt"), not by a single fixed Kind, so the
//     per-call worker labels (active handlers, claims, handlers,
//     heartbeats) are validated as a generic low-cardinality slug
//     (validLabel) instead of core's closed dialogue|judge check.
//   - The Sampler's queue gauge still reports one series per actual task
//     kind (queue_tasks{task_kind,status}), so its kind label keeps the
//     stricter dot-namespaced shape (validKind) that mirrors
//     migrations/00001_platform_tasks.sql's tasks_kind_shape CHECK — the
//     set of kinds is small and fixed by the code's kind registry, so
//     this stays low-cardinality without needing the registry itself.
//   - status gains waiting/cancelled (ADR-016 A6/B2); runs/RunStatus are
//     dropped entirely (no runs table was ported, see
//     docs/technical-discovery.md §3.5).
//   - validRole narrows to worker|maintenance|all (composite.go's Role)
//     instead of core's dialogue|judge|maintenance|all.
//   - Drops recovery_runs_total, recovery_tasks_total, and
//     finalization_runs_total: core registered these three collectors but
//     never incremented any of them (recovery.Supervisor took no
//     Observer, and finalization was never ported at all) — dead metrics,
//     not carried forward.
package observability

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/prometheus/client_golang/prometheus"
)

var ErrInvalidMetrics = errors.New("invalid observability metrics")

const metricNamespace = "emsim"

type Metrics struct {
	requests        *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
	ready           *prometheus.GaugeVec
	activeHandlers  *prometheus.GaugeVec
	claims          *prometheus.CounterVec
	handlers        *prometheus.CounterVec
	heartbeats      *prometheus.CounterVec
	queueTasks      *prometheus.GaugeVec
	samplerSamples  *prometheus.CounterVec
	process         string
}

func NewMetrics(registry prometheus.Registerer, process string) (*Metrics, error) {
	if registry == nil || !validProcess(process) {
		return nil, ErrInvalidMetrics
	}
	m := &Metrics{process: process}
	m.requests = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Name: "http_requests_total", Help: "API and admin HTTP requests."}, []string{"process", "route", "method", "status_class"})
	m.requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricNamespace, Name: "http_request_duration_seconds", Help: "API and admin HTTP request duration."}, []string{"process", "route"})
	m.ready = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: metricNamespace, Name: "process_ready", Help: "Whether a process is ready."}, []string{"process", "role"})
	m.activeHandlers = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: metricNamespace, Name: "worker_active_handlers", Help: "Active worker handlers."}, []string{"role", "pool"})
	m.claims = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Name: "worker_claims_total", Help: "Worker task claims."}, []string{"role", "pool", "outcome"})
	m.handlers = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Name: "worker_handler_total", Help: "Worker handler outcomes."}, []string{"role", "pool", "outcome"})
	m.heartbeats = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Name: "worker_heartbeat_total", Help: "Worker heartbeat outcomes."}, []string{"role", "pool", "outcome"})
	m.queueTasks = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: metricNamespace, Name: "queue_tasks", Help: "Tasks by kind and status."}, []string{"task_kind", "status"})
	m.samplerSamples = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricNamespace, Name: "sampler_samples_total", Help: "Queue sampler outcomes."}, []string{"outcome"})
	for _, collector := range []prometheus.Collector{m.requests, m.requestDuration, m.ready, m.activeHandlers, m.claims, m.handlers, m.heartbeats, m.queueTasks, m.samplerSamples} {
		if err := registry.Register(collector); err != nil {
			return nil, fmt.Errorf("register metrics: %w", err)
		}
	}
	return m, nil
}

func (m *Metrics) SetReady(role string, ready bool) {
	if m == nil || !validProcess(role) {
		return
	}
	value := 0.0
	if ready {
		value = 1
	}
	m.ready.WithLabelValues(m.process, role).Set(value)
}

func (m *Metrics) ObserveHTTP(route, method, statusClass string, seconds float64) {
	if m == nil || !validRoute(route) || !validMethod(method) || !validStatusClass(statusClass) {
		return
	}
	m.requests.WithLabelValues(m.process, route, method, statusClass).Inc()
	m.requestDuration.WithLabelValues(m.process, route).Observe(seconds)
}

func (m *Metrics) SetQueueTasks(kind, status string, count int) {
	if m == nil || !validKind(kind) || !validTaskStatus(status) || count < 0 {
		return
	}
	m.queueTasks.WithLabelValues(kind, status).Set(float64(count))
}

func (m *Metrics) ObserveSampler(outcome string) {
	if m != nil && validOutcome(outcome) {
		m.samplerSamples.WithLabelValues(outcome).Inc()
	}
}

func (m *Metrics) SetActiveHandlers(pool string, count int) {
	if m == nil || !validProcess(m.process) || !validLabel(pool) || count < 0 {
		return
	}
	m.activeHandlers.WithLabelValues(m.process, pool).Set(float64(count))
}

func (m *Metrics) ObserveClaim(pool, outcome string) {
	if m != nil && validProcess(m.process) && validLabel(pool) && validOutcome(outcome) {
		m.claims.WithLabelValues(m.process, pool, outcome).Inc()
	}
}

func (m *Metrics) ObserveHandler(pool, outcome string) {
	if m != nil && validProcess(m.process) && validLabel(pool) && validOutcome(outcome) {
		m.handlers.WithLabelValues(m.process, pool, outcome).Inc()
	}
}

func (m *Metrics) ObserveHeartbeat(pool, outcome string) {
	if m != nil && validProcess(m.process) && validLabel(pool) && validOutcome(outcome) {
		m.heartbeats.WithLabelValues(m.process, pool, outcome).Inc()
	}
}

var kindPattern = regexp.MustCompile(`^[a-z]+(\.[a-z_]+)+$`)
var labelPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
var routePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)

func validProcess(value string) bool { return value == "api" || validRole(value) }
func validRole(value string) bool {
	return value == "worker" || value == "maintenance" || value == "all"
}
func validLabel(value string) bool { return labelPattern.MatchString(value) }
func validRoute(value string) bool { return routePattern.MatchString(value) }
func validKind(value string) bool  { return kindPattern.MatchString(value) }
func validMethod(value string) bool {
	switch value {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "OTHER":
		return true
	default:
		return false
	}
}
func validStatusClass(value string) bool {
	return value == "1xx" || value == "2xx" || value == "3xx" || value == "4xx" || value == "5xx"
}
func validTaskStatus(value string) bool {
	switch value {
	case "waiting", "pending", "leased", "done", "failed", "dead_letter", "cancelled":
		return true
	default:
		return false
	}
}
func validOutcome(value string) bool {
	switch value {
	case "success", "failed", "empty", "claimed", "not_claimed", "applied", "already_applied",
		"requeued", "dead_letter", "lease_lost", "cancelled":
		return true
	default:
		return false
	}
}
