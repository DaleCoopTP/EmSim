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

const lessonColumns = `exercise_type, id, instructor_id, title, mode, level, state, epoch, timing, rubric_version, recording_grace_s, created_at, started_at, stopped_at, stop_reason, finished_at, intake_catalog_version, scoring`

func scanLesson(row pgx.Row) (training.Lesson, error) {
	var l training.Lesson
	var timingJSON, scoringJSON []byte
	err := row.Scan(&l.ExerciseType, &l.ID, &l.InstructorID, &l.Title, &l.Mode, &l.Level, &l.State,
		&l.Epoch, &timingJSON, &l.RubricVersion, &l.RecordingGraceS, &l.CreatedAt,
		&l.StartedAt, &l.StoppedAt, &l.StopReason, &l.FinishedAt, &l.IntakeCatalogVersion, &scoringJSON)
	if e := mapErr(err); e != nil {
		return training.Lesson{}, e
	}
	if err := json.Unmarshal(timingJSON, &l.Timing); err != nil {
		return training.Lesson{}, training.ErrStorage
	}
	if scoringJSON != nil {
		var scoring training.LessonScoring
		if err := json.Unmarshal(scoringJSON, &scoring); err != nil {
			return training.Lesson{}, training.ErrStorage
		}
		l.Scoring = &scoring
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

func (s *Store) UpdateLessonSettings(ctx context.Context, tx pgx.Tx, l training.Lesson) error {
	timingJSON, err := json.Marshal(l.Timing)
	if err != nil {
		return fmt.Errorf("training/postgres: marshal timing: %w", err)
	}
	var scoringJSON []byte
	if l.Scoring != nil {
		if scoringJSON, err = json.Marshal(l.Scoring); err != nil {
			return fmt.Errorf("training/postgres: marshal scoring: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE lessons SET timing = $2, scoring = $3 WHERE id = $1 AND state = 'draft'`, l.ID, timingJSON, scoringJSON)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() != 1 {
		return training.ErrConflict
	}
	return nil
}

func (s *Store) LessonByID(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock training.Lock) (training.Lesson, error) {
	query := `SELECT ` + lessonColumns + ` FROM lessons WHERE id = $1` + lockSuffix(lock, "")
	return scanLesson(tx.QueryRow(ctx, query, id))
}

// ListLessonsByInstructor excludes mode='preview' unconditionally
// (112-7/ADR-027): a preview lesson is a throwaway one-off the editor
// creates every time its author previews a draft, never a real lesson
// the instructor manages here — it would only clutter this list. Unlike
// state, this is not a caller-supplied filter; there is no way to ask
// for preview lessons back through this method.
func (s *Store) ListLessonsByInstructor(ctx context.Context, tx pgx.Tx, instructorID uuid.UUID, state *training.LessonState) ([]training.Lesson, error) {
	query := `SELECT ` + lessonColumns + ` FROM lessons WHERE instructor_id = $1 AND mode != 'preview'`
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

func (s *Store) StartLesson(ctx context.Context, tx pgx.Tx, id uuid.UUID, startedAt time.Time, intakeCatalogVersion *int) (training.Lesson, error) {
	query := `UPDATE lessons SET state = 'running', started_at = $2, intake_catalog_version = $3 WHERE id = $1 RETURNING ` + lessonColumns
	return scanLesson(tx.QueryRow(ctx, query, id, startedAt, intakeCatalogVersion))
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

// assignmentColumns' workstation_id/number are COALESCEd to zero-UUID/0
// rather than left NULL — 112-7/ADR-027's own preview assignments have no
// workstation (migrations/00017), and training.Assignment.WorkstationID/
// WorkstationNo stay plain uuid.UUID/int (not pointers) to avoid a much
// larger refactor across every other place this package already compares
// them; uuid.Nil is not a workstation any real row can have, so it is a
// safe "no workstation" sentinel here and in runSelectColumns/
// itemSelectColumns below.
const assignmentColumns = `a.lesson_id, COALESCE(a.workstation_id, '00000000-0000-0000-0000-000000000000'::uuid), COALESCE(w.number, 0), a.user_id, a.scenario_version_ids`

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
		FROM assignments a LEFT JOIN workstations w ON w.id = a.workstation_id
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
			VALUES ($1, NULLIF($2, '00000000-0000-0000-0000-000000000000'::uuid), $3, $4)
		`, lessonID, a.WorkstationID, a.UserID, a.ScenarioVersionIDs); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// ------------------------------------------------------------ runs

const runSelectColumns = `r.exercise_type, r.id, r.lesson_id, r.user_id, COALESCE(r.workstation_id, '00000000-0000-0000-0000-000000000000'::uuid), COALESCE(w.number, 0), r.mode, r.state, r.level_at_start, r.next_offer_at, r.queue_cursor, r.started_at, r.finished_at`
const runFrom = `FROM runs r LEFT JOIN workstations w ON w.id = r.workstation_id`

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
		VALUES ($1, $2, $3, $4, NULLIF($5, '00000000-0000-0000-0000-000000000000'::uuid), $6, 'active', $7, $8, $9, $10)
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

// ClearNextOfferForActiveRuns unsets next_offer_at for every still-active
// run of a lesson — Stop's own barrier (RFC-001 §7.5) calls this so a
// hard-level run's own scheduled offer can never be selected by
// RunsDueForOffer again after stop, without waiting for the durable
// lesson.close task (which may be delayed, or never run at all if the
// worker is unavailable) to finish the run first.
func (s *Store) ClearNextOfferForActiveRuns(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE runs SET next_offer_at = NULL WHERE lesson_id = $1 AND state = 'active' AND next_offer_at IS NOT NULL`, lessonID)
	if err != nil {
		return mapErr(err)
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

const itemSelectColumns = `i.id, i.run_id, r.lesson_id, r.user_id, COALESCE(w.number, 0),
	i.scenario_version_id, sv.digest, COALESCE(sv.body ->> 'target_service', ''), i.exercise_type,
	r.mode, i.ordinal, i.spawned_from, i.state, i.reaction,
	i.card, i.workflow, i.pilot_goal, i.intake_state,
	i.seq, i.stop_cutoff_log_seq, i.interruptions, i.log_seq, i.timing_effective, i.deadlines,
	i.offered_at, i.opened_at, i.primary_at, i.closed_at, i.close_reason`
const itemFrom = `FROM items i
	JOIN runs r ON r.id = i.run_id
	LEFT JOIN workstations w ON w.id = r.workstation_id
	JOIN scenario_versions sv ON sv.id = i.scenario_version_id`

func scanItem(row pgx.Row) (training.Item, error) {
	var it training.Item
	var digest []byte
	var cardJSON, workflowJSON, interruptionsJSON, timingJSON, deadlinesJSON, intakeJSON []byte
	var pilotGoal *string
	var closeReason *string
	err := row.Scan(&it.ID, &it.RunID, &it.LessonID, &it.UserID, &it.WorkstationNo,
		&it.ScenarioVersionID, &digest, &it.TargetService, &it.ExerciseType,
		&it.Mode, &it.Ordinal, &it.SpawnedFrom, &it.State, &it.Reaction,
		&cardJSON, &workflowJSON, &pilotGoal, &intakeJSON,
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
	if it.ExerciseType == "operator112_intake" {
		it.IntakeCard = &training.IntakeCard{}
		it.IntakeState = &training.IntakeState{}
		if err := json.Unmarshal(cardJSON, it.IntakeCard); err != nil {
			return training.Item{}, training.ErrStorage
		}
		if err := json.Unmarshal(intakeJSON, it.IntakeState); err != nil {
			return training.Item{}, training.ErrStorage
		}
	} else if err := json.Unmarshal(cardJSON, &it.Card); err != nil {
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
	if it.ExerciseType == "" {
		it.ExerciseType = "dds_processing"
	}
	var cardValue any = it.Card
	if it.IntakeCard != nil {
		cardValue = it.IntakeCard
	}
	cardJSON, err := json.Marshal(cardValue)
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
	var intakeJSON []byte
	if it.IntakeState != nil {
		intakeJSON, err = json.Marshal(it.IntakeState)
		if err != nil {
			return training.Item{}, fmt.Errorf("training/postgres: marshal intake state: %w", err)
		}
	}
	var pilotGoal *string
	if it.PilotGoal != "" {
		pilotGoal = &it.PilotGoal
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO items (id, run_id, scenario_version_id, ordinal, spawned_from, state, reaction, card, workflow, pilot_goal, timing_effective, deadlines, offered_at, exercise_type, intake_state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING offered_at
	`, it.ID, it.RunID, it.ScenarioVersionID, it.Ordinal, it.SpawnedFrom, it.State, it.Reaction,
		cardJSON, workflowJSON, pilotGoal, timingJSON, deadlinesJSON, it.OfferedAt, it.ExerciseType, intakeJSON).
		Scan(&it.OfferedAt)
	if err != nil {
		return training.Item{}, mapErr(err)
	}
	return it, nil
}

func (s *Store) InsertIntakeDispatch(ctx context.Context, tx pgx.Tx, dispatch training.IntakeDispatch) error {
	cardJSON, err := json.Marshal(dispatch.CardSnapshot)
	if err != nil {
		return fmt.Errorf("training/postgres: marshal dispatch card: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO intake_dispatches (item_id, action_id, service_code, card_snapshot, sent_at)
		VALUES ($1, $2, $3, $4, $5)`, dispatch.ItemID, dispatch.ActionID, dispatch.ServiceCode, cardJSON, dispatch.SentAt)
	return mapErr(err)
}

func (s *Store) IntakeDispatchByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (training.IntakeDispatch, error) {
	var d training.IntakeDispatch
	var cardJSON []byte
	err := tx.QueryRow(ctx, `SELECT item_id, action_id, service_code, card_snapshot, sent_at FROM intake_dispatches WHERE item_id = $1`, itemID).
		Scan(&d.ItemID, &d.ActionID, &d.ServiceCode, &cardJSON, &d.SentAt)
	if err != nil {
		return training.IntakeDispatch{}, mapErr(err)
	}
	if err := json.Unmarshal(cardJSON, &d.CardSnapshot); err != nil {
		return training.IntakeDispatch{}, training.ErrStorage
	}
	return d, nil
}

func (s *Store) InsertIntakeNotification(ctx context.Context, tx pgx.Tx, n training.IntakeNotification) error {
	servicesJSON, err := json.Marshal(n.Services)
	if err != nil {
		return fmt.Errorf("training/postgres: marshal notification services: %w", err)
	}
	cardJSON, err := json.Marshal(n.CardSnapshot)
	if err != nil {
		return fmt.Errorf("training/postgres: marshal notification card: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO intake_notifications (item_id, action_id, services, reason, card_snapshot, notified_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, n.ItemID, n.ActionID, servicesJSON, n.Reason, cardJSON, n.NotifiedAt)
	return mapErr(err)
}

func (s *Store) IntakeNotificationByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (training.IntakeNotification, error) {
	var n training.IntakeNotification
	var servicesJSON, cardJSON []byte
	err := tx.QueryRow(ctx, `SELECT item_id, action_id, services, reason, card_snapshot, notified_at FROM intake_notifications WHERE item_id = $1`, itemID).
		Scan(&n.ItemID, &n.ActionID, &servicesJSON, &n.Reason, &cardJSON, &n.NotifiedAt)
	if err != nil {
		return training.IntakeNotification{}, mapErr(err)
	}
	if err := json.Unmarshal(servicesJSON, &n.Services); err != nil {
		return training.IntakeNotification{}, training.ErrStorage
	}
	if err := json.Unmarshal(cardJSON, &n.CardSnapshot); err != nil {
		return training.IntakeNotification{}, training.ErrStorage
	}
	return n, nil
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
// transitions from NULL exactly once); deadlines.primary_at is replaced
// only together with the first opened_at; deadlines.complete_at gets the
// same treatment at the JSON-key level, since it lives inside the
// deadlines jsonb column rather than its own column; closed_at/
// close_reason are COALESCEd too, though in practice each item is only
// ever closed once.
func (s *Store) ApplyItemDecision(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, patch training.ItemPatch) error {
	var cardValue any = patch.Card
	if patch.IntakeCard != nil {
		cardValue = patch.IntakeCard
	}
	cardJSON, err := json.Marshal(cardValue)
	if err != nil {
		return fmt.Errorf("training/postgres: marshal card: %w", err)
	}
	var intakeJSON []byte
	if patch.IntakeState != nil {
		intakeJSON, err = json.Marshal(patch.IntakeState)
		if err != nil {
			return fmt.Errorf("training/postgres: marshal intake state: %w", err)
		}
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
			intake_state = COALESCE($12::jsonb, intake_state),
			opened_at = COALESCE(opened_at, $7),
			primary_at = COALESCE(primary_at, $8),
			deadlines = CASE
				WHEN $9::timestamptz IS NOT NULL AND (deadlines ->> 'complete_at') IS NULL
				THEN jsonb_set(deadlines, '{complete_at}', to_jsonb($9::timestamptz))
				ELSE deadlines
			END || CASE
				WHEN $13::timestamptz IS NOT NULL AND opened_at IS NULL
				THEN jsonb_build_object('primary_at', to_jsonb($13::timestamptz))
				ELSE '{}'::jsonb
			END,
			closed_at = COALESCE(closed_at, $10),
			close_reason = COALESCE(close_reason, $11)
		WHERE id = $1
	`, itemID, patch.LogSeq, patch.Seq, patch.Reaction, patch.State, cardJSON,
		patch.OpenedAt, patch.PrimaryAt, patch.CompleteAt, patch.ClosedAt, closeReason, intakeJSON, patch.PrimaryDeadline)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return training.ErrNotFound
	}
	return nil
}

// ------------------------------------------------------------ phone/media

const blobColumns = `id, sha256, mime, size, created_at`

func scanBlob(row pgx.Row) (training.Blob, error) {
	var b training.Blob
	var digest []byte
	if err := row.Scan(&b.ID, &digest, &b.MIME, &b.Size, &b.CreatedAt); err != nil {
		return training.Blob{}, mapErr(err)
	}
	if len(digest) != len(b.SHA256) {
		return training.Blob{}, training.ErrStorage
	}
	copy(b.SHA256[:], digest)
	return b, nil
}

// InsertBlob reuses an already-known digest. MIME and size form part of the
// immutable blob identity for this application, so a digest collision with
// different metadata is a conflict instead of silently reusing a row.
func (s *Store) InsertBlob(ctx context.Context, tx pgx.Tx, blob training.Blob) (training.Blob, bool, error) {
	created := false
	err := tx.QueryRow(ctx, `
		INSERT INTO blobs (id, sha256, mime, size) VALUES ($1, $2, $3, $4)
		ON CONFLICT (sha256) DO UPDATE SET sha256 = EXCLUDED.sha256
		RETURNING `+blobColumns+`, (xmax = 0)
	`, blob.ID, blob.SHA256[:], blob.MIME, blob.Size).Scan(&blob.ID, new([]byte), &blob.MIME, &blob.Size, &blob.CreatedAt, &created)
	if err != nil {
		return training.Blob{}, false, mapErr(err)
	}
	// Re-read so the digest is retained and enforce immutable metadata.
	stored, err := s.BlobBySHA256(ctx, tx, blob.SHA256)
	if err != nil {
		return training.Blob{}, false, err
	}
	if stored.MIME != blob.MIME || stored.Size != blob.Size {
		return training.Blob{}, false, training.ErrConflict
	}
	return stored, created, nil
}

func (s *Store) BlobBySHA256(ctx context.Context, tx pgx.Tx, sha256 [32]byte) (training.Blob, error) {
	return scanBlob(tx.QueryRow(ctx, `SELECT `+blobColumns+` FROM blobs WHERE sha256 = $1`, sha256[:]))
}

func (s *Store) BlobByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (training.Blob, error) {
	return scanBlob(tx.QueryRow(ctx, `SELECT `+blobColumns+` FROM blobs WHERE id = $1`, id))
}

const voiceAssetColumns = `id, scenario_version_id, key, voice, blob_id`

func scanVoiceAsset(row pgx.Row) (training.VoiceAsset, error) {
	var a training.VoiceAsset
	if err := row.Scan(&a.ID, &a.ScenarioVersionID, &a.Key, &a.Voice, &a.BlobID); err != nil {
		return training.VoiceAsset{}, mapErr(err)
	}
	return a, nil
}

func (s *Store) VoiceAssetByKey(ctx context.Context, tx pgx.Tx, scenarioVersionID uuid.UUID, key string) (training.VoiceAsset, error) {
	return scanVoiceAsset(tx.QueryRow(ctx, `SELECT `+voiceAssetColumns+` FROM voice_assets WHERE scenario_version_id=$1 AND key=$2`, scenarioVersionID, key))
}

func (s *Store) InsertVoiceAsset(ctx context.Context, tx pgx.Tx, asset training.VoiceAsset) (training.VoiceAsset, error) {
	err := tx.QueryRow(ctx, `INSERT INTO voice_assets (id, scenario_version_id, key, voice, blob_id) VALUES ($1,$2,$3,$4,$5) RETURNING `+voiceAssetColumns,
		asset.ID, asset.ScenarioVersionID, asset.Key, asset.Voice, asset.BlobID).Scan(&asset.ID, &asset.ScenarioVersionID, &asset.Key, &asset.Voice, &asset.BlobID)
	if err != nil {
		return training.VoiceAsset{}, mapErr(err)
	}
	return asset, nil
}

const callColumns = `id, item_id, contact_key, direction, event_key, started_at, ended_at, reaction_at_call, blob_id, accepted_by, summary, recording_sha256, recording_size, recording_mime, recording_state, recording_upload_deadline_at, recording_received_at`

func scanCall(row pgx.Row) (training.Call, error) {
	var c training.Call
	var digest []byte
	var size *int64
	var mime *string
	var eventKey *string
	if err := row.Scan(&c.ID, &c.ItemID, &c.ContactKey, &c.Direction, &eventKey, &c.StartedAt, &c.EndedAt, &c.ReactionAtCall, &c.BlobID, &c.AcceptedBy, &c.Summary, &digest, &size, &mime, &c.RecordingState, &c.RecordingUploadDeadlineAt, &c.RecordingReceivedAt); err != nil {
		return training.Call{}, mapErr(err)
	}
	if eventKey != nil {
		c.EventKey = *eventKey
	}
	if len(digest) > 0 {
		if len(digest) != 32 || size == nil || mime == nil {
			return training.Call{}, training.ErrStorage
		}
		var m training.RecordingManifest
		copy(m.SHA256[:], digest)
		m.Size = *size
		m.MIME = *mime
		c.Recording = &m
	}
	return c, nil
}

func (s *Store) InsertCall(ctx context.Context, tx pgx.Tx, call training.Call) (training.Call, error) {
	direction := call.Direction
	if direction == "" {
		direction = training.CallOutgoing
	}
	var eventKey *string
	if call.EventKey != "" {
		eventKey = &call.EventKey
	}
	returned := tx.QueryRow(ctx, `INSERT INTO calls (id,item_id,contact_key,direction,event_key,started_at,reaction_at_call) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+callColumns,
		call.ID, call.ItemID, call.ContactKey, direction, eventKey, call.StartedAt, call.ReactionAtCall)
	return scanCall(returned)
}

func (s *Store) CallByID(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock training.Lock) (training.Call, error) {
	return scanCall(tx.QueryRow(ctx, `SELECT `+callColumns+` FROM calls WHERE id=$1`+lockSuffix(lock, ""), id))
}

func (s *Store) CallsByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) ([]training.Call, error) {
	rows, err := tx.Query(ctx, `SELECT `+callColumns+` FROM calls WHERE item_id=$1 ORDER BY started_at,id`, itemID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var calls []training.Call
	for rows.Next() {
		c, err := scanCall(rows)
		if err != nil {
			return nil, err
		}
		calls = append(calls, c)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return calls, nil
}

func (s *Store) EndCall(ctx context.Context, tx pgx.Tx, id uuid.UUID, endedAt time.Time, acceptedBy, summary string, recording *training.RecordingManifest) error {
	state := training.RecordingAbsent
	var sha []byte
	var size any
	var mime any
	if recording != nil {
		state = training.RecordingAwaiting
		sha = recording.SHA256[:]
		size = recording.Size
		mime = recording.MIME
	}
	tag, err := tx.Exec(ctx, `UPDATE calls SET ended_at=$2, accepted_by=$3, summary=$4, recording_sha256=$5, recording_size=$6, recording_mime=$7, recording_state=$8 WHERE id=$1 AND ended_at IS NULL`, id, endedAt, acceptedBy, summary, sha, size, mime, state)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return training.ErrNotFound
	}
	return nil
}

func (s *Store) SetCallRecordingDeadline(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, deadline time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE calls SET recording_upload_deadline_at=$2 WHERE item_id=$1 AND recording_state='awaiting' AND recording_upload_deadline_at IS NULL`, itemID, deadline)
	return mapErr(err)
}

func (s *Store) SetCallRecordingReady(ctx context.Context, tx pgx.Tx, id, blobID uuid.UUID, receivedAt time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE calls SET blob_id=$2, recording_received_at=$3, recording_state='ready' WHERE id=$1 AND recording_state='awaiting'`, id, blobID, receivedAt)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return training.ErrNotFound
	}
	return nil
}

// RunningLessonIDs lists every lesson currently in state='running' — an
// unlocked read (Service.Recover locks each one individually before
// recovering it).
func (s *Store) RunningLessonIDs(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM lessons WHERE state = 'running'`)
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

// RecoverOpenItems appends entry to items.interruptions for every open
// item of lessonID (RFC-001 §7.2), skipping any item that already
// carries entry.RecoveryID so a retried call cannot duplicate the
// marker. entry is marshaled once and appended verbatim via jsonb
// concatenation — offered_at/deadlines/due_at are never touched by this
// statement. Scoped by lessonID alone (not lessons.state): the caller
// already holds that lesson's row FOR UPDATE and has already verified
// it is running (Service.Recover's own per-lesson barrier).
func (s *Store) RecoverOpenItems(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID, entry training.Interruption) ([]uuid.UUID, error) {
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("training/postgres: marshal interruption: %w", err)
	}
	rows, err := tx.Query(ctx, `
		UPDATE items SET interruptions = items.interruptions || jsonb_build_array($1::jsonb)
		FROM runs
		WHERE items.run_id = runs.id AND runs.lesson_id = $3
		  AND items.state IN ('offered', 'opened', 'in_progress')
		  AND NOT EXISTS (
		    SELECT 1 FROM jsonb_array_elements(items.interruptions) elem
		    WHERE elem ->> 'recovery_id' = $2
		  )
		RETURNING items.id
	`, entryJSON, entry.RecoveryID.String(), lessonID)
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

func (s *Store) EvidenceByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (training.EvidenceBody, [32]byte, error) {
	raw, digestArray, err := s.EvidenceDocumentByItem(ctx, tx, itemID)
	if err != nil {
		return training.EvidenceBody{}, [32]byte{}, err
	}
	var body training.EvidenceBody
	var discriminator struct {
		ExerciseType string `json:"exercise_type"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return training.EvidenceBody{}, [32]byte{}, training.ErrStorage
	}
	if discriminator.ExerciseType == "operator112_intake" {
		// Assessment's manual path needs ancestry and the digest, while its
		// review endpoint reads the complete variant through the raw port.
		var identity struct {
			ItemID            uuid.UUID `json:"item_id"`
			RunID             uuid.UUID `json:"run_id"`
			LessonID          uuid.UUID `json:"lesson_id"`
			TraineeID         uuid.UUID `json:"trainee_id"`
			ScenarioVersionID uuid.UUID `json:"scenario_version_id"`
			ScenarioDigest    string    `json:"scenario_digest"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil {
			return training.EvidenceBody{}, [32]byte{}, training.ErrStorage
		}
		body.ItemID, body.RunID, body.LessonID, body.TraineeID = identity.ItemID, identity.RunID, identity.LessonID, identity.TraineeID
		body.ScenarioVersionID, body.ScenarioDigest, body.ExerciseType = identity.ScenarioVersionID, identity.ScenarioDigest, "operator112_intake"
	} else if err := json.Unmarshal(raw, &body); err != nil {
		return training.EvidenceBody{}, [32]byte{}, training.ErrStorage
	}
	return body, digestArray, nil
}

func (s *Store) EvidenceDocumentByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (json.RawMessage, [32]byte, error) {
	var raw []byte
	var digest []byte
	if err := tx.QueryRow(ctx, `SELECT body, digest FROM evidence WHERE item_id = $1`, itemID).Scan(&raw, &digest); err != nil {
		return nil, [32]byte{}, mapErr(err)
	}
	var digestArray [32]byte
	copy(digestArray[:], digest)
	return json.RawMessage(raw), digestArray, nil
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
