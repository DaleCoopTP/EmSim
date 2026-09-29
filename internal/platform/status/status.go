// Package status is the administrator's view of a running installation
// (ADR-033): schema version, the task queue, model health, disk space and
// backups. It reads platform tables only (tasks, platform_heartbeats,
// goose_db_version) and never a task's payload or a lesson's content —
// the administrator has no access to training data (RFC-001 §9).
//
// The api cannot see everything itself: it has no route to the inference
// network and does not mount the backup directory. The worker's Prober
// records what it observes into platform_heartbeats, and Snapshot reads
// that back.
package status

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrStorage = errors.New("status storage failure")

// Heartbeat statuses.
const (
	StatusOK          = "ok"
	StatusUnavailable = "unavailable"
)

// Components the worker reports (platform_heartbeats.component).
const (
	ComponentLLM       = "llm"
	ComponentBackup    = "backup"
	ComponentIntegrity = "integrity"
	// ComponentSTT is written by the api itself (it alone knows STT_URL).
	ComponentSTT = "stt"
	// A worker's own heartbeat is "worker.<id>".
	ComponentWorkerPrefix = "worker."
)

// Heartbeat is one platform_heartbeats row.
type Heartbeat struct {
	Component string
	Status    string
	Detail    map[string]any
	CheckedAt time.Time
}

// TaskSummary is a task as the status screen shows it: identity, kind and
// outcome, never its payload or result.
type TaskSummary struct {
	ID            uuid.UUID
	Kind          string
	Status        string
	Attempts      int
	MaxAttempts   int
	LastErrorCode *string
	CreatedAt     time.Time
	TerminalAt    *time.Time
}

// Snapshot is everything GET /admin/status reports from the database.
type Snapshot struct {
	SchemaVersion int64
	Tasks         map[string]map[string]int
	Heartbeats    []Heartbeat
	FailedTasks   []TaskSummary
	BackupRuns    []TaskSummary
	LastBackupAt  *time.Time
	ServerTime    time.Time
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// UpsertHeartbeat records the latest observation of one component, by
// PostgreSQL's clock.
func (s *Store) UpsertHeartbeat(ctx context.Context, component, status string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return ErrStorage
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO platform_heartbeats (component, status, detail, checked_at)
		VALUES ($1, $2, $3, clock_timestamp())
		ON CONFLICT (component) DO UPDATE SET status = EXCLUDED.status, detail = EXCLUDED.detail, checked_at = EXCLUDED.checked_at`,
		component, status, encoded); err != nil {
		return ErrStorage
	}
	return nil
}

// Heartbeat returns one component's latest observation.
func (s *Store) Heartbeat(ctx context.Context, component string) (Heartbeat, bool, error) {
	var hb Heartbeat
	var detail []byte
	err := s.pool.QueryRow(ctx, `SELECT component, status, detail, checked_at FROM platform_heartbeats WHERE component = $1`, component).
		Scan(&hb.Component, &hb.Status, &detail, &hb.CheckedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Heartbeat{}, false, nil
	}
	if err != nil || json.Unmarshal(detail, &hb.Detail) != nil {
		return Heartbeat{}, false, ErrStorage
	}
	return hb, true, nil
}

// Snapshot reads the whole status picture. backupKind names the backup
// task kind (its owner, cmd/emsim, registers it).
func (s *Store) Snapshot(ctx context.Context, backupKind string) (Snapshot, error) {
	snapshot := Snapshot{Tasks: map[string]map[string]int{}}
	if err := s.pool.QueryRow(ctx, `SELECT clock_timestamp(), COALESCE((SELECT MAX(version_id) FROM goose_db_version WHERE is_applied), 0)`).
		Scan(&snapshot.ServerTime, &snapshot.SchemaVersion); err != nil {
		return Snapshot{}, ErrStorage
	}
	rows, err := s.pool.Query(ctx, `SELECT kind, status, count(*) FROM tasks GROUP BY kind, status ORDER BY kind, status`)
	if err != nil {
		return Snapshot{}, ErrStorage
	}
	for rows.Next() {
		var kind, taskStatus string
		var count int
		if err := rows.Scan(&kind, &taskStatus, &count); err != nil {
			rows.Close()
			return Snapshot{}, ErrStorage
		}
		if snapshot.Tasks[kind] == nil {
			snapshot.Tasks[kind] = map[string]int{}
		}
		snapshot.Tasks[kind][taskStatus] = count
	}
	rows.Close()
	if rows.Err() != nil {
		return Snapshot{}, ErrStorage
	}

	rows, err = s.pool.Query(ctx, `SELECT component, status, detail, checked_at FROM platform_heartbeats ORDER BY component`)
	if err != nil {
		return Snapshot{}, ErrStorage
	}
	for rows.Next() {
		var hb Heartbeat
		var detail []byte
		if err := rows.Scan(&hb.Component, &hb.Status, &detail, &hb.CheckedAt); err != nil || json.Unmarshal(detail, &hb.Detail) != nil {
			rows.Close()
			return Snapshot{}, ErrStorage
		}
		snapshot.Heartbeats = append(snapshot.Heartbeats, hb)
	}
	rows.Close()
	if rows.Err() != nil {
		return Snapshot{}, ErrStorage
	}

	if snapshot.FailedTasks, err = s.tasks(ctx, `WHERE status IN ('failed', 'dead_letter') ORDER BY terminal_at DESC NULLS LAST, id LIMIT 50`); err != nil {
		return Snapshot{}, err
	}
	if snapshot.BackupRuns, err = s.tasks(ctx, `WHERE kind = $1 ORDER BY created_at DESC, id LIMIT 10`, backupKind); err != nil {
		return Snapshot{}, err
	}
	var last *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT MAX(terminal_at) FROM tasks WHERE kind = $1 AND status = 'done'`, backupKind).Scan(&last); err != nil {
		return Snapshot{}, ErrStorage
	}
	snapshot.LastBackupAt = last
	return snapshot, nil
}

func (s *Store) tasks(ctx context.Context, where string, args ...any) ([]TaskSummary, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kind, status, attempts, max_attempts, last_error_code, created_at, terminal_at FROM tasks `+where, args...)
	if err != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	var out []TaskSummary
	for rows.Next() {
		var task TaskSummary
		if err := rows.Scan(&task.ID, &task.Kind, &task.Status, &task.Attempts, &task.MaxAttempts, &task.LastErrorCode, &task.CreatedAt, &task.TerminalAt); err != nil {
			return nil, ErrStorage
		}
		out = append(out, task)
	}
	if rows.Err() != nil {
		return nil, ErrStorage
	}
	return out, nil
}

// ActiveTaskExists reports whether a task of kind is queued or running.
func (s *Store) ActiveTaskExists(ctx context.Context, kind string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT FROM tasks WHERE kind = $1 AND status IN ('pending', 'leased'))`, kind).Scan(&exists); err != nil {
		return false, ErrStorage
	}
	return exists, nil
}
