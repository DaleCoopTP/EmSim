package status

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"emsim/internal/platform/audit"
	"emsim/internal/platform/config"
	"emsim/internal/platform/httpapi"
	"emsim/internal/platform/maintenance"
	"emsim/internal/platform/tasks"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Queue is what the admin endpoints need from the task queue;
// *tasks.Store satisfies it.
type Queue interface {
	EnqueueTx(context.Context, pgx.Tx, tasks.EnqueueRequest) (uuid.UUID, bool, error)
	RetryTx(context.Context, pgx.Tx, uuid.UUID, map[tasks.Kind]tasks.RetryGuard) (tasks.RetryCandidate, tasks.RetryTarget, error)
}

// Actor names who made a request (the admin's session), for audit.
type Actor func(context.Context) (userID uuid.UUID, role string, ok bool)

// Handlers serve GET /admin/status and POST /admin/backup. Access control
// is the caller's: Register wraps every route with protect, which the
// api composes from the auth module's session and admin-role checks.
type Handlers struct {
	pool           *pgxpool.Pool
	store          *Store
	queue          Queue
	retryGuards    map[tasks.Kind]tasks.RetryGuard
	backupKind     tasks.Kind
	integrityKind  tasks.Kind
	blobRoot       string
	expectedSchema int64
	actor          Actor
	logins         LoginResolver
	build          string
	load           LoadSources
	config         []config.Param
}

// NewHandlers: retryGuards are the per-kind checks an admin retry must
// pass (a kind without one is simply retried).
func NewHandlers(pool *pgxpool.Pool, queue Queue, retryGuards map[tasks.Kind]tasks.RetryGuard, backupKind tasks.Kind, blobRoot string, expectedSchema int64, actor Actor) *Handlers {
	return &Handlers{pool: pool, store: NewStore(pool), queue: queue, retryGuards: retryGuards, backupKind: backupKind, blobRoot: blobRoot, expectedSchema: expectedSchema, actor: actor}
}

// WithIntegrity names the queue kind of the integrity check, enabling
// POST /admin/integrity (ADR-038).
func (h *Handlers) WithIntegrity(kind tasks.Kind) *Handlers {
	h.integrityKind = kind
	return h
}

// WithBuild sets the api's own build version, shown on the status screen
// next to each worker's (ADR-038).
func (h *Handlers) WithBuild(version string) *Handlers {
	h.build = version
	return h
}

func (h *Handlers) Register(mux *http.ServeMux, protect func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/admin/status", protect(h.getStatus))
	mux.Handle("POST /api/v1/admin/backup", protect(h.startBackup))
	mux.Handle("POST /api/v1/admin/integrity", protect(h.startIntegrity))
	mux.Handle("POST /api/v1/admin/tasks/{taskId}/retry", protect(h.retryTask))
	mux.Handle("PUT /api/v1/admin/maintenance", protect(h.setMaintenance))
	mux.Handle("GET /api/v1/admin/config", protect(h.getConfig))
	mux.Handle("GET /api/v1/admin/failures", protect(h.getFailures))
	mux.Handle("GET /api/v1/admin/failures.csv", protect(h.exportFailures))
	mux.Handle("GET /api/v1/admin/audit", protect(h.listAudit))
	mux.Handle("GET /api/v1/admin/audit.csv", protect(h.exportAudit))
}

type backupCopyJSON struct {
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
}

type taskJSON struct {
	ID            uuid.UUID  `json:"id"`
	Kind          string     `json:"kind"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	MaxAttempts   int        `json:"max_attempts"`
	LastErrorCode *string    `json:"last_error_code"`
	CreatedAt     time.Time  `json:"created_at"`
	TerminalAt    *time.Time `json:"terminal_at"`
}

type modelJSON struct {
	Status    string    `json:"status"`
	Model     string    `json:"model,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

type backupJSON struct {
	Enabled      bool             `json:"enabled"`
	FreeBytes    *int64           `json:"free_bytes"`
	CheckedAt    *time.Time       `json:"checked_at"`
	LastBackupAt *time.Time       `json:"last_backup_at"`
	Copies       []backupCopyJSON `json:"copies"`
	Runs         []taskJSON       `json:"runs"`
}

// integrityJSON is the last integrity check's report as the worker stored
// it (ids and counts only); nil until the first run.
type integrityJSON struct {
	CheckedAt time.Time        `json:"checked_at"`
	OK        bool             `json:"ok"`
	Sections  []map[string]any `json:"sections"`
}

type workerJSON struct {
	ID        string    `json:"id"`
	Role      string    `json:"role,omitempty"`
	Version   string    `json:"version,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

type statusJSON struct {
	Build                 string                    `json:"build"`
	ServerTime            time.Time                 `json:"server_time"`
	DBSchemaVersion       int64                     `json:"db_schema_version"`
	ExpectedSchemaVersion int64                     `json:"expected_schema_version"`
	Tasks                 map[string]map[string]int `json:"tasks"`
	Models                map[string]*modelJSON     `json:"models"`
	DiskFreeBytes         *int64                    `json:"disk_free_bytes"`
	LastBackupAt          *time.Time                `json:"last_backup_at"`
	Load                  loadJSON                  `json:"load"`
	Maintenance           maintenanceJSON           `json:"maintenance"`
	Backup                backupJSON                `json:"backup"`
	Integrity             *integrityJSON            `json:"integrity"`
	Workers               []workerJSON              `json:"workers"`
	FailedTasks           []taskJSON                `json:"failed_tasks"`
}

func (h *Handlers) getStatus(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.store.Snapshot(r.Context(), string(h.backupKind))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to read status", nil)
		return
	}
	body := statusJSON{
		Build: h.build, Load: h.loadSnapshot(r.Context()), ServerTime: snapshot.ServerTime, DBSchemaVersion: snapshot.SchemaVersion, ExpectedSchemaVersion: h.expectedSchema,
		Tasks:        snapshot.Tasks,
		Models:       map[string]*modelJSON{"llm": nil, "stt": nil, "tts": nil},
		Backup:       backupJSON{LastBackupAt: snapshot.LastBackupAt, Copies: []backupCopyJSON{}, Runs: toTasksJSON(snapshot.BackupRuns)},
		LastBackupAt: snapshot.LastBackupAt, Workers: []workerJSON{}, FailedTasks: toTasksJSON(snapshot.FailedTasks),
	}
	if state, err := maintenance.Get(r.Context(), h.pool); err == nil {
		body.Maintenance = toMaintenanceJSON(state)
	}
	if free, err := FreeBytes(h.blobRoot); err == nil && h.blobRoot != "" {
		body.DiskFreeBytes = &free
	}
	for _, hb := range snapshot.Heartbeats {
		switch {
		case hb.Component == ComponentLLM:
			model, _ := hb.Detail["model"].(string)
			body.Models["llm"] = &modelJSON{Status: hb.Status, Model: model, CheckedAt: hb.CheckedAt}
		case hb.Component == ComponentSTT:
			model, _ := hb.Detail["model"].(string)
			body.Models["stt"] = &modelJSON{Status: hb.Status, Model: model, CheckedAt: hb.CheckedAt}
		case hb.Component == ComponentBackup:
			checked := hb.CheckedAt
			body.Backup.Enabled, body.Backup.CheckedAt = true, &checked
			var detail struct {
				FreeBytes *int64           `json:"free_bytes"`
				Copies    []backupCopyJSON `json:"copies"`
			}
			if raw, err := json.Marshal(hb.Detail); err == nil && json.Unmarshal(raw, &detail) == nil {
				body.Backup.FreeBytes = detail.FreeBytes
				if detail.Copies != nil {
					body.Backup.Copies = detail.Copies
				}
			}
		case hb.Component == ComponentIntegrity:
			report := &integrityJSON{CheckedAt: hb.CheckedAt, OK: hb.Status == StatusOK, Sections: []map[string]any{}}
			if raw, err := json.Marshal(hb.Detail["sections"]); err == nil {
				_ = json.Unmarshal(raw, &report.Sections)
			}
			if at, ok := hb.Detail["at"].(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
					report.CheckedAt = t
				}
			}
			body.Integrity = report
		case strings.HasPrefix(hb.Component, ComponentWorkerPrefix):
			role, _ := hb.Detail["role"].(string)
			version, _ := hb.Detail["version"].(string)
			body.Workers = append(body.Workers, workerJSON{ID: strings.TrimPrefix(hb.Component, ComponentWorkerPrefix), Role: role, Version: version, CheckedAt: hb.CheckedAt})
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func toTasksJSON(in []TaskSummary) []taskJSON {
	out := make([]taskJSON, 0, len(in))
	for _, task := range in {
		out = append(out, taskJSON(task))
	}
	return out
}

// startBackup queues one backup now. It is refused while no worker has
// reported a backup directory (BACKUP_DIR unset) and while another backup
// is still queued or running.
func (h *Handlers) startBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, ok, err := h.store.Heartbeat(ctx, ComponentBackup); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to start backup", nil)
		return
	} else if !ok {
		httpapi.WriteError(w, r, httpapi.CodeConflict, "backups are not configured on the worker", map[string]any{"reason": "backup_not_configured"})
		return
	}
	h.enqueueManual(w, r, h.backupKind, "admin.backup.start", "backup_in_progress", "a backup is already queued or running")
}

// startIntegrity queues one integrity check now (ADR-038), refused while
// another is queued or running.
func (h *Handlers) startIntegrity(w http.ResponseWriter, r *http.Request) {
	if h.integrityKind == "" {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "integrity check is not available", nil)
		return
	}
	h.enqueueManual(w, r, h.integrityKind, "admin.integrity.start", "integrity_in_progress", "an integrity check is already queued or running")
}

// enqueueManual queues one manual run of a system task and audits it in
// the same transaction.
func (h *Handlers) enqueueManual(w http.ResponseWriter, r *http.Request, kind tasks.Kind, auditAction, busyReason, busyMessage string) {
	ctx := r.Context()
	if active, err := h.store.ActiveTaskExists(ctx, string(kind)); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to start task", nil)
		return
	} else if active {
		httpapi.WriteError(w, r, httpapi.CodeConflict, busyMessage, map[string]any{"reason": busyReason})
		return
	}
	actorID, role, ok := h.actor(ctx)
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "authentication required", nil)
		return
	}
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to start task", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	taskID := uuid.New()
	id, _, err := h.queue.EnqueueTx(ctx, tx, tasks.EnqueueRequest{
		TaskID: taskID, Kind: kind, ScopeType: "system",
		DedupKey: string(kind) + ":manual:" + taskID.String(), NextAttemptAt: time.Now().UTC(),
	})
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to start task", nil)
		return
	}
	if err := audit.Record(ctx, tx, audit.Entry{
		ActorID: &actorID, ActorRole: role, Action: auditAction, ResourceType: "task", ResourceID: &id,
		Outcome: audit.OutcomeOK, RequestID: httpapi.RequestIDFromContext(ctx),
	}); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to start task", nil)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to start task", nil)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task_id": id})
}

// retryTask continues a failed or dead_letter task (ADR-033): same id,
// one more attempt in its budget. Refused (409 not_retryable) for any
// other status and wherever the kind's guard says a repeat would be wrong.
func (h *Handlers) retryTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	taskID, err := uuid.Parse(r.PathValue("taskId"))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "task not found", nil)
		return
	}
	actorID, role, ok := h.actor(ctx)
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "authentication required", nil)
		return
	}
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to retry task", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, target, err := h.queue.RetryTx(ctx, tx, taskID, h.retryGuards)
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		httpapi.WriteError(w, r, httpapi.CodeNotFound, "task not found", nil)
		return
	case errors.Is(err, tasks.ErrNotRetryable):
		httpapi.WriteError(w, r, httpapi.CodeConflict, "this task cannot be retried", map[string]any{"reason": "not_retryable"})
		return
	case err != nil:
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to retry task", nil)
		return
	}
	if err := audit.Record(ctx, tx, audit.Entry{
		ActorID: &actorID, ActorRole: role, Action: "admin.task.retry", ResourceType: "task", ResourceID: &task.ID,
		Outcome: audit.OutcomeOK, RequestID: httpapi.RequestIDFromContext(ctx),
		Details: map[string]any{"kind": string(task.Kind), "from": string(task.Status), "to": string(target)},
	}); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to retry task", nil)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to retry task", nil)
		return
	}
	retried, err := h.store.tasks(ctx, `WHERE id = $1`, taskID)
	if err != nil || len(retried) != 1 {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to read task", nil)
		return
	}
	writeJSON(w, http.StatusOK, taskJSON(retried[0]))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
