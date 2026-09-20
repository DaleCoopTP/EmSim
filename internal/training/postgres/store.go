// Package postgres is the pgx-backed adapter for the training module's
// own Store port (internal/training/store.go) — the same shape
// internal/content/postgres and internal/auth/postgres already use:
// every method but WithTx takes an explicit pgx.Tx, the caller owns
// commit/rollback, and an error this package cannot map to a specific
// domain sentinel comes back as training.ErrNotFound/ErrStorage/
// ErrConflict.
package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"emsim/internal/platform/audit"
	"emsim/internal/training"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// *Store structurally satisfies training.Store — asserted here so a
// divergence between the two fails the build at the adapter, matching
// internal/content/postgres.Store's own assertion.
var _ training.Store = (*Store)(nil)

func (s *Store) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return training.ErrStorage
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return training.ErrStorage
	}
	return nil
}

// Now is RFC-001's authoritative clock — clock_timestamp(), not now()/
// CURRENT_TIMESTAMP (which freeze at transaction start).
func (s *Store) Now(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, training.ErrStorage
	}
	return now, nil
}

func (s *Store) AuditRecord(ctx context.Context, tx pgx.Tx, entry audit.Entry) error {
	return audit.Record(ctx, tx, entry)
}

// mapErr turns a driver-level error into training's own sentinels — the
// two this package cannot say more about (ErrNotFound for no matching
// row, ErrStorage for anything else) plus ErrConflict for a unique-
// constraint violation, since several tables here (runs' one-active-
// per-user/workstation indexes, actions' command_id) rely on the
// database itself as the final authority alongside a service-layer
// pre-check.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return training.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return training.ErrConflict
	}
	return training.ErrStorage
}

// lockSuffix builds the trailing "FOR SHARE"/"FOR UPDATE" clause RFC-001
// §7.1/§8's explicit lock order needs. of names the table alias to scope
// the lock to on a joined query ("" for a single-table query, where
// PostgreSQL needs no OF clause).
func lockSuffix(lock training.Lock, of string) string {
	suffix := ""
	switch lock {
	case training.LockShare:
		suffix = " FOR SHARE"
	case training.LockUpdate:
		suffix = " FOR UPDATE"
	default:
		return ""
	}
	if of != "" {
		suffix += " OF " + of
	}
	return suffix
}

// ------------------------------------------------------------ lessons

const lessonColumns = `exercise_type, id, instructor_id, title, mode, level, state, epoch, timing, rubric_version, recording_grace_s, created_at, started_at, stopped_at, stop_reason, finished_at`

func scanLesson(row pgx.Row) (training.Lesson, error) {
	var l training.Lesson
	var timingJSON []byte
	err := row.Scan(&l.ExerciseType, &l.ID, &l.InstructorID, &l.Title, &l.Mode, &l.Level, &l.State,
		&l.Epoch, &timingJSON, &l.RubricVersion, &l.RecordingGraceS, &l.CreatedAt,
		&l.StartedAt, &l.StoppedAt, &l.StopReason, &l.FinishedAt)
	if e := mapErr(err); e != nil {
		return training.Lesson{}, e
	}
	if err := json.Unmarshal(timingJSON, &l.Timing); err != nil {
		return training.Lesson{}, training.ErrStorage
	}
	return l, nil
}

func (s *Store) InsertLesson(ctx context.Context, tx pgx.Tx, l training.Lesson) (training.Lesson, error) {
	timingJSON, err := json.Marshal(l.Timing)
	if err != nil {
		return training.Lesson{}, fmt.Errorf("training/postgres: marshal timing: %w", err)
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO lessons (exercise_type, id, instructor_id, title, mode, level, state, timing, rubric_version, recording_grace_s)
		VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7, $8, $9)
		RETURNING created_at
	`, l.ExerciseType, l.ID, l.InstructorID, l.Title, l.Mode, l.Level, timingJSON, l.RubricVersion, l.RecordingGraceS).
		Scan(&l.CreatedAt)
	if err != nil {
		return training.Lesson{}, mapErr(err)
	}
	l.State = training.LessonDraft
	return l, nil
}

func (s *Store) LessonByID(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock training.Lock) (training.Lesson, error) {
	query := `SELECT ` + lessonColumns + ` FROM lessons WHERE id = $1` + lockSuffix(lock, "")
	return scanLesson(tx.QueryRow(ctx, query, id))
}

func (s *Store) ListLessonsByInstructor(ctx context.Context, tx pgx.Tx, instructorID uuid.UUID, state *training.LessonState) ([]training.Lesson, error) {
	query := `SELECT ` + lessonColumns + ` FROM lessons WHERE instructor_id = $1`
	args := []any{instructorID}
	if state != nil {
		query += ` AND state = $2`
		args = append(args, *state)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var lessons []training.Lesson
	for rows.Next() {
		l, err := scanLesson(rows)
		if err != nil {
			return nil, err
		}
		lessons = append(lessons, l)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return lessons, nil
}

func (s *Store) StartLesson(ctx context.Context, tx pgx.Tx, id uuid.UUID, startedAt time.Time) (training.Lesson, error) {
	query := `UPDATE lessons SET state = 'running', started_at = $2 WHERE id = $1 RETURNING ` + lessonColumns
	return scanLesson(tx.QueryRow(ctx, query, id, startedAt))
}

func (s *Store) StopLesson(ctx context.Context, tx pgx.Tx, id uuid.UUID, stoppedAt time.Time, reason *string, epoch int64) (training.Lesson, error) {
	query := `
		UPDATE lessons SET state = 'stopped', stopped_at = $2, stop_reason = $3, epoch = $4
		WHERE id = $1 AND state = 'running'
		RETURNING ` + lessonColumns
	return scanLesson(tx.QueryRow(ctx, query, id, stoppedAt, reason, epoch))
}

func (s *Store) SetStopCutoffForOpenItems(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		UPDATE items SET stop_cutoff_log_seq = items.log_seq
		FROM runs
		WHERE items.run_id = runs.id AND runs.lesson_id = $1
		  AND items.state IN ('offered', 'opened', 'in_progress')
		  AND items.stop_cutoff_log_seq IS NULL
		RETURNING items.id
	`, lessonID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, mapErr(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return ids, nil
}

// ------------------------------------------------------------ assignments

const assignmentColumns = `a.lesson_id, a.workstation_id, w.number, a.user_id, a.scenario_version_ids`

func scanAssignment(row pgx.Row) (training.Assignment, error) {
	var a training.Assignment
	err := row.Scan(&a.LessonID, &a.WorkstationID, &a.WorkstationNo, &a.UserID, &a.ScenarioVersionIDs)
	if e := mapErr(err); e != nil {
		return training.Assignment{}, e
	}
	return a, nil
}

func (s *Store) AssignmentsByLesson(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID) ([]training.Assignment, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+assignmentColumns+`
		FROM assignments a JOIN workstations w ON w.id = a.workstation_id
		WHERE a.lesson_id = $1
		ORDER BY w.number
	`, lessonID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var assignments []training.Assignment
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, a)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return assignments, nil
}

// ReplaceAssignments deletes and re-inserts lessonID's assignments — the
// application service only ever calls this on a draft lesson (checked
// under LockUpdate before this runs), so there is no concurrent reader
// to race.
func (s *Store) ReplaceAssignments(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID, assignments []training.Assignment) error {
	if _, err := tx.Exec(ctx, `DELETE FROM assignments WHERE lesson_id = $1`, lessonID); err != nil {
		return mapErr(err)
	}
	for _, a := range assignments {
		if _, err := tx.Exec(ctx, `
			INSERT INTO assignments (lesson_id, workstation_id, user_id, scenario_version_ids)
			VALUES ($1, $2, $3, $4)
		`, lessonID, a.WorkstationID, a.UserID, a.ScenarioVersionIDs); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// ------------------------------------------------------------ runs

const runSelectColumns = `r.exercise_type, r.id, r.lesson_id, r.user_id, r.workstation_id, w.number, r.mode, r.state, r.level_at_start, r.next_offer_at, r.queue_cursor, r.started_at, r.finished_at`
const runFrom = `FROM runs r JOIN workstations w ON w.id = r.workstation_id`

func scanRun(row pgx.Row) (training.Run, error) {
	var r training.Run
	err := row.Scan(&r.ExerciseType, &r.ID, &r.LessonID, &r.UserID, &r.WorkstationID, &r.WorkstationNo,
		&r.Mode, &r.State, &r.LevelAtStart, &r.NextOfferAt, &r.QueueCursor, &r.StartedAt, &r.FinishedAt)
	if e := mapErr(err); e != nil {
		return training.Run{}, e
	}
	return r, nil
}

func (s *Store) InsertRun(ctx context.Context, tx pgx.Tx, r training.Run) (training.Run, error) {
	err := tx.QueryRow(ctx, `
		INSERT INTO runs (exercise_type, id, lesson_id, user_id, workstation_id, mode, state, level_at_start, next_offer_at, queue_cursor, started_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'active', $7, $8, $9, $10)
		RETURNING started_at
	`, r.ExerciseType, r.ID, r.LessonID, r.UserID, r.WorkstationID, r.Mode, r.LevelAtStart, r.NextOfferAt, r.QueueCursor, r.StartedAt).
		Scan(&r.StartedAt)
	if err != nil {
		return training.Run{}, mapErr(err)
	}
	r.State = training.RunActive
	return r, nil
}

func (s *Store) RunByID(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock training.Lock) (training.Run, error) {
	query := `SELECT ` + runSelectColumns + ` ` + runFrom + ` WHERE r.id = $1` + lockSuffix(lock, "r")
	return scanRun(tx.QueryRow(ctx, query, id))
}

func (s *Store) ActiveRunByUser(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (training.Run, error) {
	query := `SELECT ` + runSelectColumns + ` ` + runFrom + ` WHERE r.user_id = $1 AND r.state = 'active'`
	return scanRun(tx.QueryRow(ctx, query, userID))
}

func (s *Store) RunsByLesson(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID) ([]training.Run, error) {
	query := `SELECT ` + runSelectColumns + ` ` + runFrom + ` WHERE r.lesson_id = $1 ORDER BY w.number`
	rows, err := tx.Query(ctx, query, lessonID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var runs []training.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return runs, nil
}

func (s *Store) RunsDueForOffer(ctx context.Context, tx pgx.Tx, now time.Time) ([]training.Run, error) {
	rows, err := tx.Query(ctx, `SELECT `+runSelectColumns+` `+runFrom+`
		WHERE r.state = 'active' AND r.next_offer_at IS NOT NULL AND r.next_offer_at <= $1
		ORDER BY r.next_offer_at, r.id`, now)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var runs []training.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return runs, nil
}

func (s *Store) SetRunQueueCursor(ctx context.Context, tx pgx.Tx, id uuid.UUID, cursor int) error {
	tag, err := tx.Exec(ctx, `UPDATE runs SET queue_cursor = $2 WHERE id = $1`, id, cursor)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return training.ErrNotFound
	}
	return nil
}

func (s *Store) SetRunNextOfferAt(ctx context.Context, tx pgx.Tx, id uuid.UUID, nextOfferAt *time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE runs SET next_offer_at = $2 WHERE id = $1`, id, nextOfferAt)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return training.ErrNotFound
	}
	return nil
}

func (s *Store) FinishRun(ctx context.Context, tx pgx.Tx, id uuid.UUID, finishedAt time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE runs SET state = 'finished', finished_at = $2 WHERE id = $1`, id, finishedAt)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return training.ErrNotFound
	}
	return nil
}

// FinishLesson transitions a lesson to finished from either state it can
// naturally reach that from: running (every run's queue exhausted while
// the lesson was still live, slice 3) or stopped (C8's
// CloseStoppedLesson, once every item the stop barrier left open has
// been interrupted). draft can never finish, and finished is already
// terminal, so this WHERE clause covers every real caller without
// needing a second, near-identical method.
func (s *Store) FinishLesson(ctx context.Context, tx pgx.Tx, id uuid.UUID, finishedAt time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE lessons SET state = 'finished', finished_at = $2 WHERE id = $1 AND state IN ('running', 'stopped')`, id, finishedAt)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return training.ErrNotFound
	}
	return nil
}

// ------------------------------------------------------------ items

const itemSelectColumns = `i.id, i.run_id, r.lesson_id, r.user_id, w.number,
	i.scenario_version_id, sv.digest, sv.body ->> 'target_service',
	r.mode, i.ordinal, i.spawned_from, i.state, i.reaction,
	i.card, i.workflow, i.pilot_goal,
	i.seq, i.stop_cutoff_log_seq, i.interruptions, i.log_seq, i.timing_effective, i.deadlines,
	i.offered_at, i.opened_at, i.primary_at, i.closed_at, i.close_reason`
const itemFrom = `FROM items i
	JOIN runs r ON r.id = i.run_id
	JOIN workstations w ON w.id = r.workstation_id
	JOIN scenario_versions sv ON sv.id = i.scenario_version_id`

func scanItem(row pgx.Row) (training.Item, error) {
	var it training.Item
	var digest []byte
	var cardJSON, workflowJSON, interruptionsJSON, timingJSON, deadlinesJSON []byte
	var pilotGoal *string
	var closeReason *string
	err := row.Scan(&it.ID, &it.RunID, &it.LessonID, &it.UserID, &it.WorkstationNo,
		&it.ScenarioVersionID, &digest, &it.TargetService,
		&it.Mode, &it.Ordinal, &it.SpawnedFrom, &it.State, &it.Reaction,
		&cardJSON, &workflowJSON, &pilotGoal,
		&it.Seq, &it.StopCutoffLogSeq, &interruptionsJSON, &it.LogSeq, &timingJSON, &deadlinesJSON,
		&it.OfferedAt, &it.OpenedAt, &it.PrimaryAt, &it.ClosedAt, &closeReason)
	if e := mapErr(err); e != nil {
		return training.Item{}, e
	}
	it.ScenarioDigest = hex.EncodeToString(digest)
	if pilotGoal != nil {
		it.PilotGoal = *pilotGoal
	}
	if closeReason != nil {
		cr := training.CloseReason(*closeReason)
		it.CloseReason = &cr
	}
	if err := json.Unmarshal(cardJSON, &it.Card); err != nil {
		return training.Item{}, training.ErrStorage
	}
	if err := json.Unmarshal(workflowJSON, &it.Workflow); err != nil {
		return training.Item{}, training.ErrStorage
	}
	if err := json.Unmarshal(interruptionsJSON, &it.Interruptions); err != nil {
		return training.Item{}, training.ErrStorage
	}
	if err := json.Unmarshal(timingJSON, &it.TimingEffective); err != nil {
		return training.Item{}, training.ErrStorage
	}
	if err := json.Unmarshal(deadlinesJSON, &it.Deadlines); err != nil {
		return training.Item{}, training.ErrStorage
	}
	return it, nil
}

func (s *Store) InsertItem(ctx context.Context, tx pgx.Tx, it training.Item) (training.Item, error) {
	cardJSON, err := json.Marshal(it.Card)
	if err != nil {
		return training.Item{}, fmt.Errorf("training/postgres: marshal card: %w", err)
	}
	workflowJSON, err := json.Marshal(it.Workflow)
	if err != nil {
		return training.Item{}, fmt.Errorf("training/postgres: marshal workflow: %w", err)
	}
	timingJSON, err := json.Marshal(it.TimingEffective)
	if err != nil {
		return training.Item{}, fmt.Errorf("training/postgres: marshal timing_effective: %w", err)
	}
	deadlinesJSON, err := json.Marshal(it.Deadlines)
	if err != nil {
		return training.Item{}, fmt.Errorf("training/postgres: marshal deadlines: %w", err)
	}
	var pilotGoal *string
	if it.PilotGoal != "" {
		pilotGoal = &it.PilotGoal
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO items (id, run_id, scenario_version_id, ordinal, spawned_from, state, reaction, card, workflow, pilot_goal, timing_effective, deadlines, offered_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING offered_at
	`, it.ID, it.RunID, it.ScenarioVersionID, it.Ordinal, it.SpawnedFrom, it.State, it.Reaction,
		cardJSON, workflowJSON, pilotGoal, timingJSON, deadlinesJSON, it.OfferedAt).
		Scan(&it.OfferedAt)
	if err != nil {
		return training.Item{}, mapErr(err)
	}
	return it, nil
}

func (s *Store) ItemByID(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock training.Lock) (training.Item, error) {
	query := `SELECT ` + itemSelectColumns + ` ` + itemFrom + ` WHERE i.id = $1` + lockSuffix(lock, "i")
	return scanItem(tx.QueryRow(ctx, query, id))
}

func (s *Store) ItemsByRun(ctx context.Context, tx pgx.Tx, runID uuid.UUID) ([]training.Item, error) {
	query := `SELECT ` + itemSelectColumns + ` ` + itemFrom + ` WHERE i.run_id = $1 ORDER BY i.ordinal`
	rows, err := tx.Query(ctx, query, runID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var items []training.Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return items, nil
}

// ApplyItemDecision writes one command attempt's effect on an item.
// log_seq/seq/reaction/state/card are written unconditionally — on a
// rejected Decision these equal the item's own current values
// (training.Decision's own documented contract), so writing them back
// is a harmless no-op. opened_at/primary_at are COALESCEd (each
// transitions from NULL exactly once); deadlines.complete_at gets the
// same treatment at the JSON-key level, since it lives inside the
// deadlines jsonb column rather than its own column; closed_at/
// close_reason are COALESCEd too, though in practice each item is only
// ever closed once.
func (s *Store) ApplyItemDecision(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, patch training.ItemPatch) error {
	cardJSON, err := json.Marshal(patch.Card)
	if err != nil {
		return fmt.Errorf("training/postgres: marshal card: %w", err)
	}
	var closeReason *string
	if patch.CloseReason != nil {
		cr := string(*patch.CloseReason)
		closeReason = &cr
	}
	tag, err := tx.Exec(ctx, `
		UPDATE items SET
			log_seq = $2,
			seq = $3,
			reaction = $4,
			state = $5,
			card = $6,
			opened_at = COALESCE(opened_at, $7),
			primary_at = COALESCE(primary_at, $8),
			deadlines = CASE
				WHEN $9::timestamptz IS NOT NULL AND (deadlines ->> 'complete_at') IS NULL
				THEN jsonb_set(deadlines, '{complete_at}', to_jsonb($9::timestamptz))
				ELSE deadlines
			END,
			closed_at = COALESCE(closed_at, $10),
			close_reason = COALESCE(close_reason, $11)
		WHERE id = $1
	`, itemID, patch.LogSeq, patch.Seq, patch.Reaction, patch.State, cardJSON,
		patch.OpenedAt, patch.PrimaryAt, patch.CompleteAt, patch.ClosedAt, closeReason)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return training.ErrNotFound
	}
	return nil
}

// RecoverOpenItems appends entry to items.interruptions for every open
// item of a running lesson (RFC-001 §7.2), skipping any item that
// already carries entry.RecoveryID so a retried call cannot duplicate
// the marker. entry is marshaled once and appended verbatim via jsonb
// concatenation — offered_at/deadlines/due_at are never touched by this
// statement.
func (s *Store) RecoverOpenItems(ctx context.Context, tx pgx.Tx, entry training.Interruption) ([]uuid.UUID, error) {
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("training/postgres: marshal interruption: %w", err)
	}
	rows, err := tx.Query(ctx, `
		UPDATE items SET interruptions = items.interruptions || jsonb_build_array($1::jsonb)
		FROM runs, lessons
		WHERE items.run_id = runs.id AND runs.lesson_id = lessons.id
		  AND lessons.state = 'running'
		  AND items.state IN ('offered', 'opened', 'in_progress')
		  AND NOT EXISTS (
		    SELECT 1 FROM jsonb_array_elements(items.interruptions) elem
		    WHERE elem ->> 'recovery_id' = $2
		  )
		RETURNING items.id
	`, entryJSON, entry.RecoveryID.String())
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, mapErr(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return ids, nil
}

// ------------------------------------------------------------ actions

const actionColumns = `item_id, seq, log_seq, actor_id, request_digest, id, command_id, type, payload, effect, accepted, rejection, receipt, http_status, client_at, server_at`

func scanAction(row pgx.Row) (training.Action, error) {
	var a training.Action
	var digest, payloadJSON, effectJSON, receiptJSON []byte
	var rejection *string
	err := row.Scan(&a.ItemID, &a.Seq, &a.LogSeq, &a.ActorID, &digest, &a.ID, &a.CommandID, &a.Type,
		&payloadJSON, &effectJSON, &a.Accepted, &rejection, &receiptJSON, &a.HTTPStatus, &a.ClientAt, &a.ServerAt)
	if e := mapErr(err); e != nil {
		return training.Action{}, e
	}
	if len(digest) != len(a.RequestDigest) {
		return training.Action{}, training.ErrStorage
	}
	copy(a.RequestDigest[:], digest)
	a.Payload = payloadJSON
	if rejection != nil {
		a.Rejection = training.Rejection(*rejection)
	}
	if err := json.Unmarshal(effectJSON, &a.Effect); err != nil {
		return training.Action{}, training.ErrStorage
	}
	if err := json.Unmarshal(receiptJSON, &a.Receipt); err != nil {
		return training.Action{}, training.ErrStorage
	}
	return a, nil
}

func (s *Store) ActionByCommandID(ctx context.Context, tx pgx.Tx, commandID uuid.UUID) (training.Action, error) {
	return scanAction(tx.QueryRow(ctx, `SELECT `+actionColumns+` FROM actions WHERE command_id = $1`, commandID))
}

func (s *Store) InsertAction(ctx context.Context, tx pgx.Tx, a training.Action) (training.Action, error) {
	payloadJSON := []byte(a.Payload)
	if len(payloadJSON) == 0 {
		payloadJSON = []byte(`{}`)
	}
	effectJSON := []byte(`{}`)
	if len(a.Effect) > 0 {
		var err error
		effectJSON, err = json.Marshal(a.Effect)
		if err != nil {
			return training.Action{}, fmt.Errorf("training/postgres: marshal effect: %w", err)
		}
	}
	receiptJSON, err := json.Marshal(a.Receipt)
	if err != nil {
		return training.Action{}, fmt.Errorf("training/postgres: marshal receipt: %w", err)
	}
	var rejection *string
	if a.Rejection != "" {
		r := string(a.Rejection)
		rejection = &r
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO actions (item_id, seq, log_seq, actor_id, request_digest, id, command_id, type, payload, effect, accepted, rejection, receipt, http_status, client_at, server_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`, a.ItemID, a.Seq, a.LogSeq, a.ActorID, a.RequestDigest[:], a.ID, a.CommandID, a.Type,
		payloadJSON, effectJSON, a.Accepted, rejection, receiptJSON, a.HTTPStatus, a.ClientAt, a.ServerAt)
	if err != nil {
		return training.Action{}, mapErr(err)
	}
	return a, nil
}

func (s *Store) ActionsByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) ([]training.Action, error) {
	rows, err := tx.Query(ctx, `SELECT `+actionColumns+` FROM actions WHERE item_id = $1 ORDER BY log_seq`, itemID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var actions []training.Action
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		actions = append(actions, a)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return actions, nil
}

func (s *Store) LastActionByRun(ctx context.Context, tx pgx.Tx, runID uuid.UUID) (training.Action, error) {
	row := tx.QueryRow(ctx, `
		SELECT a.item_id, a.seq, a.log_seq, a.actor_id, a.request_digest, a.id, a.command_id, a.type,
			a.payload, a.effect, a.accepted, a.rejection, a.receipt, a.http_status, a.client_at, a.server_at
		FROM actions a
		JOIN items i ON i.id = a.item_id
		WHERE i.run_id = $1
		ORDER BY a.server_at DESC, a.log_seq DESC
		LIMIT 1
	`, runID)
	return scanAction(row)
}

// ------------------------------------------------------------ evidence

func (s *Store) InsertEvidence(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, ev training.Evidence) error {
	_, err := tx.Exec(ctx, `INSERT INTO evidence (item_id, body, digest) VALUES ($1, $2, $3)`,
		itemID, ev.Body, ev.Digest[:])
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// ------------------------------------------------------------ events and reports

const itemEventColumns = `id, item_id, event_key, anchor_at, due_at, state, delivered_at, late, skip_reason`

func scanItemEvent(row pgx.Row) (training.ItemEvent, error) {
	var event training.ItemEvent
	if err := row.Scan(&event.ID, &event.ItemID, &event.EventKey, &event.AnchorAt, &event.DueAt,
		&event.State, &event.DeliveredAt, &event.Late, &event.SkipReason); err != nil {
		return training.ItemEvent{}, mapErr(err)
	}
	return event, nil
}

func (s *Store) InsertItemEvent(ctx context.Context, tx pgx.Tx, event training.ItemEvent) (training.ItemEvent, error) {
	_, err := tx.Exec(ctx, `
		INSERT INTO item_events (id, item_id, event_key, anchor_at, due_at, state, delivered_at, late, skip_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, event.ID, event.ItemID, event.EventKey, event.AnchorAt, event.DueAt, event.State,
		event.DeliveredAt, event.Late, event.SkipReason)
	if err != nil {
		return training.ItemEvent{}, mapErr(err)
	}
	return event, nil
}

func (s *Store) ItemEventsByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) ([]training.ItemEvent, error) {
	rows, err := tx.Query(ctx, `SELECT `+itemEventColumns+` FROM item_events WHERE item_id = $1 ORDER BY due_at, event_key`, itemID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var events []training.ItemEvent
	for rows.Next() {
		event, err := scanItemEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return events, nil
}

func (s *Store) ItemEventByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (training.ItemEvent, error) {
	return scanItemEvent(tx.QueryRow(ctx, `SELECT `+itemEventColumns+` FROM item_events WHERE id = $1`, id))
}

func (s *Store) ScheduledItemEventsDue(ctx context.Context, tx pgx.Tx, now time.Time) ([]training.ItemEvent, error) {
	rows, err := tx.Query(ctx, `SELECT `+itemEventColumns+` FROM item_events WHERE state = 'scheduled' AND due_at <= $1 ORDER BY due_at, id`, now)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var events []training.ItemEvent
	for rows.Next() {
		event, err := scanItemEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return events, nil
}

func (s *Store) DeliverItemEvent(ctx context.Context, tx pgx.Tx, id uuid.UUID, deliveredAt time.Time, late bool) error {
	tag, err := tx.Exec(ctx, `UPDATE item_events SET state='delivered', delivered_at=$2, late=$3 WHERE id=$1 AND state='scheduled'`, id, deliveredAt, late)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return training.ErrConflict
	}
	return nil
}

func (s *Store) SkipItemEvent(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string) error {
	tag, err := tx.Exec(ctx, `UPDATE item_events SET state='skipped', skip_reason=$2 WHERE id=$1 AND state='scheduled'`, id, reason)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return training.ErrConflict
	}
	return nil
}

func (s *Store) SkipRemainingItemEvents(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx, `UPDATE item_events SET state='skipped', skip_reason=$2 WHERE item_id=$1 AND state='scheduled'`, itemID, reason)
	if err != nil {
		return mapErr(err)
	}
	return nil
}

const controlReportColumns = `id, item_id, action_id, text, created_at`

func scanControlReport(row pgx.Row) (training.ControlReport, error) {
	var report training.ControlReport
	if err := row.Scan(&report.ID, &report.ItemID, &report.ActionID, &report.Text, &report.CreatedAt); err != nil {
		return training.ControlReport{}, mapErr(err)
	}
	return report, nil
}

func (s *Store) InsertControlReport(ctx context.Context, tx pgx.Tx, report training.ControlReport) (training.ControlReport, error) {
	_, err := tx.Exec(ctx, `INSERT INTO control_reports (id, item_id, action_id, text, created_at) VALUES ($1,$2,$3,$4,$5)`,
		report.ID, report.ItemID, report.ActionID, report.Text, report.CreatedAt)
	if err != nil {
		return training.ControlReport{}, mapErr(err)
	}
	return report, nil
}

func (s *Store) ControlReportsByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) ([]training.ControlReport, error) {
	rows, err := tx.Query(ctx, `SELECT `+controlReportColumns+` FROM control_reports WHERE item_id = $1 ORDER BY created_at, id`, itemID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var reports []training.ControlReport
	for rows.Next() {
		report, err := scanControlReport(rows)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return reports, nil
}
