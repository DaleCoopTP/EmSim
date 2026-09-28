package status

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"emsim/internal/platform/audit"
	"emsim/internal/platform/httpapi"
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
	blobRoot       string
	expectedSchema int64
	actor          Actor
}

// NewHandlers: retryGuards are the per-kind checks an admin retry must
// pass (a kind without one is simply retried).
func NewHandlers(pool *pgxpool.Pool, queue Queue, retryGuards map[tasks.Kind]tasks.RetryGuard, backupKind tasks.Kind, blobRoot string, expectedSchema int64, actor Actor) *Handlers {
	return &Handlers{pool: pool, store: NewStore(pool), queue: queue, retryGuards: retryGuards, backupKind: backupKind, blobRoot: blobRoot, expectedSchema: expectedSchema, actor: actor}
}

func (h *Handlers) Register(mux *http.ServeMux, protect func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/admin/status", protect(h.getStatus))
	mux.Handle("POST /api/v1/admin/backup", protect(h.startBackup))
	mux.Handle("POST /api/v1/admin/tasks/{taskId}/retry", protect(h.retryTask))
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

type workerJSON struct {
	ID        string    `json:"id"`
	Role      string    `json:"role,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

type statusJSON struct {
	ServerTime            time.Time                 `json:"server_time"`
	DBSchemaVersion       int64                     `json:"db_schema_version"`
	ExpectedSchemaVersion int64                     `json:"expected_schema_version"`
	Tasks                 map[string]map[string]int `json:"tasks"`
	Models                map[string]*modelJSON     `json:"models"`
	DiskFreeBytes         *int64                    `json:"disk_free_bytes"`
	LastBackupAt          *time.Time                `json:"last_backup_at"`
	Backup                backupJSON                `json:"backup"`
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
		ServerTime: snapshot.ServerTime, DBSchemaVersion: snapshot.SchemaVersion, ExpectedSchemaVersion: h.expectedSchema,
		Tasks:        snapshot.Tasks,
		Models:       map[string]*modelJSON{"llm": nil, "stt": nil, "tts": nil},
		Backup:       backupJSON{LastBackupAt: snapshot.LastBackupAt, Copies: []backupCopyJSON{}, Runs: toTasksJSON(snapshot.BackupRuns)},
		LastBackupAt: snapshot.LastBackupAt, Workers: []workerJSON{}, FailedTasks: toTasksJSON(snapshot.FailedTasks),
	}
	if free, err := FreeBytes(h.blobRoot); err == nil && h.blobRoot != "" {
		body.DiskFreeBytes = &free
	}
	for _, hb := range snapshot.Heartbeats {
		switch {
		case hb.Component == ComponentLLM:
			model, _ := hb.Detail["model"].(string)
			body.Models["llm"] = &modelJSON{Status: hb.Status, Model: model, CheckedAt: hb.CheckedAt}
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
		case strings.HasPrefix(hb.Component, ComponentWorkerPrefix):
			role, _ := hb.Detail["role"].(string)
			body.Workers = append(body.Workers, workerJSON{ID: strings.TrimPrefix(hb.Component, ComponentWorkerPrefix), Role: role, CheckedAt: hb.CheckedAt})
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
	if active, err := h.store.ActiveTaskExists(ctx, string(h.backupKind)); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to start backup", nil)
		return
	} else if active {
		httpapi.WriteError(w, r, httpapi.CodeConflict, "a backup is already queued or running", map[string]any{"reason": "backup_in_progress"})
		return
	}
	actorID, role, ok := h.actor(ctx)
	if !ok {
		httpapi.WriteError(w, r, httpapi.CodeUnauthorized, "authentication required", nil)
		return
	}
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to start backup", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	taskID := uuid.New()
	id, _, err := h.queue.EnqueueTx(ctx, tx, tasks.EnqueueRequest{
		TaskID: taskID, Kind: h.backupKind, ScopeType: "system",
		DedupKey: string(h.backupKind) + ":manual:" + taskID.String(), NextAttemptAt: time.Now().UTC(),
	})
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to start backup", nil)
		return
	}
	if err := audit.Record(ctx, tx, audit.Entry{
		ActorID: &actorID, ActorRole: role, Action: "admin.backup.start", ResourceType: "task", ResourceID: &id,
		Outcome: audit.OutcomeOK, RequestID: httpapi.RequestIDFromContext(ctx),
	}); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to start backup", nil)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to start backup", nil)
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
