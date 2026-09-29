// Package postgres is the pgx-backed adapter for the content module's
// Store port (internal/content/store.go) — the same shape internal/auth's
// own postgres adapter uses (see its package doc): every method but
// WithTx takes an explicit pgx.Tx, the caller owns commit/rollback, and
// an error this package cannot map to a specific domain sentinel comes
// back as content.ErrNotFound or content.ErrStorage.
package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"emsim/internal/content"
	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// *Store structurally satisfies content.Store — asserted here so a
// divergence between the two fails the build at the adapter, matching
// internal/auth/postgres.Store's own assertion.
var _ content.Store = (*Store)(nil)

func (s *Store) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return content.ErrStorage
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return content.ErrStorage
	}
	return nil
}

// ------------------------------------------------------------ services

const serviceColumns = `code, name, workflow, active`

func scanService(row pgx.Row) (content.ServiceRecord, error) {
	var s content.ServiceRecord
	var workflowJSON []byte
	err := row.Scan(&s.Code, &s.Name, &workflowJSON, &s.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return content.ServiceRecord{}, content.ErrNotFound
	}
	if err != nil {
		return content.ServiceRecord{}, content.ErrStorage
	}
	if err := json.Unmarshal(workflowJSON, &s.Workflow); err != nil {
		return content.ServiceRecord{}, content.ErrStorage
	}
	return s, nil
}

func (s *Store) ServiceByCode(ctx context.Context, tx pgx.Tx, code string) (content.ServiceRecord, error) {
	return scanService(tx.QueryRow(ctx, `SELECT `+serviceColumns+` FROM services WHERE code = $1`, code))
}

func (s *Store) ListServices(ctx context.Context, tx pgx.Tx) ([]content.ServiceRecord, error) {
	rows, err := tx.Query(ctx, `SELECT `+serviceColumns+` FROM services ORDER BY code`)
	if err != nil {
		return nil, content.ErrStorage
	}
	defer rows.Close()
	var records []content.ServiceRecord
	for rows.Next() {
		r, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, content.ErrStorage
	}
	return records, nil
}

func (s *Store) InsertService(ctx context.Context, tx pgx.Tx, rec content.ServiceRecord) error {
	workflowJSON, err := json.Marshal(rec.Workflow)
	if err != nil {
		return content.ErrStorage
	}
	_, err = tx.Exec(ctx, `INSERT INTO services (code, name, workflow, active) VALUES ($1, $2, $3, $4)`,
		rec.Code, rec.Name, workflowJSON, rec.Active)
	if err != nil {
		return content.ErrStorage
	}
	return nil
}

// ------------------------------------------------------- classifier_types

const classifierColumns = `id, code, name, features, notify, source_row, imported_at`

func scanClassifierType(row pgx.Row) (content.ClassifierType, error) {
	var c content.ClassifierType
	var featuresJSON []byte
	err := row.Scan(&c.ID, &c.Code, &c.Name, &featuresJSON, &c.Notify, &c.SourceRow, &c.ImportedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return content.ClassifierType{}, content.ErrNotFound
	}
	if err != nil {
		return content.ClassifierType{}, content.ErrStorage
	}
	if err := json.Unmarshal(featuresJSON, &c.Features); err != nil {
		return content.ClassifierType{}, content.ErrStorage
	}
	return c, nil
}

func (s *Store) ClassifierTypeByCode(ctx context.Context, tx pgx.Tx, code string) (content.ClassifierType, error) {
	return scanClassifierType(tx.QueryRow(ctx, `SELECT `+classifierColumns+` FROM classifier_types WHERE code = $1`, code))
}

func (s *Store) InsertClassifierType(ctx context.Context, tx pgx.Tx, c content.ClassifierType) error {
	featuresJSON, err := json.Marshal(c.Features)
	if err != nil {
		return content.ErrStorage
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO classifier_types (id, code, name, features, notify, source_row)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, c.ID, c.Code, c.Name, featuresJSON, c.Notify, c.SourceRow)
	if err != nil {
		return content.ErrStorage
	}
	return nil
}

// ------------------------------------------------------------ scenarios

const scenarioColumns = `id, title, COALESCE(target_service, ''), difficulty, origin, ticket_id, status, source_key, created_by, created_at, updated_at`

func scanScenario(row pgx.Row) (content.ScenarioRecord, error) {
	var rec content.ScenarioRecord
	err := row.Scan(&rec.ID, &rec.Title, &rec.TargetService, &rec.Difficulty, &rec.Origin,
		&rec.TicketID, &rec.Status, &rec.SourceKey, &rec.CreatedBy, &rec.CreatedAt, &rec.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return content.ScenarioRecord{}, content.ErrNotFound
	}
	if err != nil {
		return content.ScenarioRecord{}, content.ErrStorage
	}
	return rec, nil
}

// ScenarioByKey locks the row FOR UPDATE — see content.Store's doc
// comment on why: a concurrent import of the same key must serialize.
func (s *Store) ScenarioByKey(ctx context.Context, tx pgx.Tx, key string) (content.ScenarioRecord, error) {
	return scanScenario(tx.QueryRow(ctx, `SELECT `+scenarioColumns+` FROM scenarios WHERE source_key = $1 FOR UPDATE`, key))
}

func (s *Store) ScenarioByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (content.ScenarioRecord, error) {
	return scanScenario(tx.QueryRow(ctx, `SELECT `+scenarioColumns+` FROM scenarios WHERE id = $1`, id))
}

func (s *Store) ScenarioByIDForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (content.ScenarioRecord, error) {
	return scanScenario(tx.QueryRow(ctx, `SELECT `+scenarioColumns+` FROM scenarios WHERE id = $1 FOR UPDATE`, id))
}

func (s *Store) InsertScenario(ctx context.Context, tx pgx.Tx, rec content.ScenarioRecord) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO scenarios (id, title, target_service, difficulty, origin, ticket_id, status, source_key, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, rec.ID, rec.Title, nullableString(rec.TargetService), rec.Difficulty, rec.Origin, rec.TicketID, rec.Status, rec.SourceKey, rec.CreatedBy)
	if err != nil {
		return content.ErrStorage
	}
	return nil
}

func (s *Store) UpdateScenarioDifficulty(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID, difficulty int) error {
	_, err := tx.Exec(ctx, `UPDATE scenarios SET difficulty = $2, updated_at = clock_timestamp() WHERE id = $1`,
		scenarioID, difficulty)
	if err != nil {
		return content.ErrStorage
	}
	return nil
}

func (s *Store) UpdateScenarioTitle(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID, title string) error {
	_, err := tx.Exec(ctx, `UPDATE scenarios SET title = $2, updated_at = clock_timestamp() WHERE id = $1`,
		scenarioID, title)
	if err != nil {
		return content.ErrStorage
	}
	return nil
}

func (s *Store) UpdateScenarioStatus(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID, status string) error {
	_, err := tx.Exec(ctx, `UPDATE scenarios SET status = $2, updated_at = clock_timestamp() WHERE id = $1`,
		scenarioID, status)
	if err != nil {
		return content.ErrStorage
	}
	return nil
}

// ListScenarios implements content.Store.ListScenarios's filter/paging
// contract, joined to each scenario's current approved version for the
// version number and has_events a ScenarioSummary carries (slice 2 gives
// every stored scenario exactly one approved version — see
// content.Service.ImportScenarios).
func (s *Store) ListScenarios(ctx context.Context, tx pgx.Tx, filter content.ScenarioFilter) ([]content.ScenarioSummary, int, error) {
	page, pageSize := filter.Page, filter.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	targetService := nullableString(filter.TargetService)
	exerciseType := nullableString(string(filter.ExerciseType))
	status := nullableString(filter.Status)
	difficultyMin := nullableInt(filter.DifficultyMin)
	difficultyMax := nullableInt(filter.DifficultyMax)
	requestingUserID := nullableUUID(filter.RequestingUserID)

	// sv picks the approved version when one exists (everyone's normal
	// view); only for the requesting caller's own scenario, absent any
	// approved version, it falls back to that scenario's own latest
	// version instead (112-7/ADR-027: "Мои черновики" — see
	// content.ScenarioFilter.RequestingUserID's own doc comment for why
	// this is not a plain INNER JOIN). Any other scenario with no
	// approved version stays invisible, exactly as before this change.
	const where = `
		FROM scenarios s
		LEFT JOIN LATERAL (
			SELECT * FROM scenario_versions sv2
			WHERE sv2.scenario_id = s.id
			  AND (sv2.status = 'approved' OR ($6::uuid IS NOT NULL AND s.created_by = $6))
			ORDER BY (sv2.status = 'approved') DESC, sv2.version DESC
			LIMIT 1
		) sv ON true
		WHERE sv.id IS NOT NULL
		  AND ($1::text IS NULL OR s.target_service = $1)
		  AND (($2::text IS NULL AND s.status <> 'archived') OR s.status = $2)
		  AND ($3::int IS NULL OR s.difficulty >= $3)
		  AND ($4::int IS NULL OR s.difficulty <= $4)
		  AND ($5::text IS NULL OR sv.exercise_type = $5)
	`

	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) `+where, targetService, status, difficultyMin, difficultyMax, exerciseType, requestingUserID).Scan(&total); err != nil {
		return nil, 0, content.ErrStorage
	}

	rows, err := tx.Query(ctx, `
		SELECT s.id, s.title, COALESCE(s.target_service, ''), s.difficulty, s.origin, s.ticket_id, s.status, s.source_key,
		       s.created_by, s.created_at, s.updated_at,
		       sv.exercise_type, sv.version, jsonb_array_length(COALESCE(sv.body->'events', '[]'::jsonb)) > 0
		`+where+`
		ORDER BY s.updated_at DESC, s.id
		LIMIT $7 OFFSET $8
	`, targetService, status, difficultyMin, difficultyMax, exerciseType, requestingUserID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, content.ErrStorage
	}
	defer rows.Close()

	var summaries []content.ScenarioSummary
	for rows.Next() {
		var sum content.ScenarioSummary
		if err := rows.Scan(&sum.ID, &sum.Title, &sum.TargetService, &sum.Difficulty, &sum.Origin, &sum.TicketID,
			&sum.Status, &sum.SourceKey, &sum.CreatedBy, &sum.CreatedAt, &sum.UpdatedAt, &sum.ExerciseType, &sum.Version, &sum.HasEvents); err != nil {
			return nil, 0, content.ErrStorage
		}
		summaries = append(summaries, sum)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, content.ErrStorage
	}
	return summaries, total, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// --------------------------------------------------- scenario_versions

func scanScenarioVersion(row pgx.Row) (content.ScenarioVersionRecord, error) {
	var v content.ScenarioVersionRecord
	var bodyJSON, digest []byte
	err := row.Scan(&v.ID, &v.ScenarioID, &v.Version, &v.Status, &bodyJSON, &digest, &v.Difficulty,
		&v.SourceTaskID, &v.PromptRef, &v.CreatedBy, &v.CreatedAt, &v.ApprovedBy, &v.ApprovedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return content.ScenarioVersionRecord{}, content.ErrNotFound
	}
	if err != nil {
		return content.ScenarioVersionRecord{}, content.ErrStorage
	}
	if err := json.Unmarshal(bodyJSON, &v.Body); err != nil {
		return content.ScenarioVersionRecord{}, content.ErrStorage
	}
	v.BodyJSON = bodyJSON
	if len(digest) != len(v.Digest) {
		return content.ScenarioVersionRecord{}, content.ErrStorage
	}
	copy(v.Digest[:], digest)
	return v, nil
}

const scenarioVersionColumns = `id, scenario_id, version, status, body, digest, difficulty, source_task_id, prompt_ref, created_by, created_at, approved_by, approved_at`

func (s *Store) MaxVersion(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID) (int, error) {
	var max int
	err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM scenario_versions WHERE scenario_id = $1`, scenarioID).Scan(&max)
	if err != nil {
		return 0, content.ErrStorage
	}
	return max, nil
}

func (s *Store) VersionReferenceByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (content.ScenarioVersionReference, error) {
	var ref content.ScenarioVersionReference
	err := tx.QueryRow(ctx, `
		SELECT sv.status,
		       sv.approved_by IS NOT NULL AND sv.approved_at IS NOT NULL AS published,
		       sv.exercise_type, COALESCE(s.target_service, '')
		FROM scenario_versions sv
		JOIN scenarios s ON s.id = sv.scenario_id
		WHERE sv.id = $1
	`, id).Scan(&ref.Status, &ref.Published, &ref.ExerciseType, &ref.TargetService)
	if errors.Is(err, pgx.ErrNoRows) {
		return content.ScenarioVersionReference{}, content.ErrNotFound
	}
	if err != nil {
		return content.ScenarioVersionReference{}, content.ErrStorage
	}
	return ref, nil
}

func (s *Store) VersionReferenceBySourceKeyVersion(ctx context.Context, tx pgx.Tx, key string, version int) (content.ScenarioVersionReference, error) {
	var ref content.ScenarioVersionReference
	err := tx.QueryRow(ctx, `
		SELECT sv.status,
		       sv.approved_by IS NOT NULL AND sv.approved_at IS NOT NULL AS published,
		       sv.exercise_type, COALESCE(s.target_service, '')
		FROM scenario_versions sv
		JOIN scenarios s ON s.id = sv.scenario_id
		WHERE s.source_key = $1 AND sv.version = $2
	`, key, version).Scan(&ref.Status, &ref.Published, &ref.ExerciseType, &ref.TargetService)
	if errors.Is(err, pgx.ErrNoRows) {
		return content.ScenarioVersionReference{}, content.ErrNotFound
	}
	if err != nil {
		return content.ScenarioVersionReference{}, content.ErrStorage
	}
	return ref, nil
}

func (s *Store) VersionByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (content.ScenarioVersionRecord, error) {
	return scanScenarioVersion(tx.QueryRow(ctx,
		`SELECT `+scenarioVersionColumns+` FROM scenario_versions WHERE id = $1`, id))
}

func (s *Store) VersionByNumber(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID, version int) (content.ScenarioVersionRecord, error) {
	return scanScenarioVersion(tx.QueryRow(ctx,
		`SELECT `+scenarioVersionColumns+` FROM scenario_versions WHERE scenario_id = $1 AND version = $2`,
		scenarioID, version))
}

func (s *Store) ApprovedVersion(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID) (content.ScenarioVersionRecord, error) {
	return scanScenarioVersion(tx.QueryRow(ctx,
		`SELECT `+scenarioVersionColumns+` FROM scenario_versions WHERE scenario_id = $1 AND status = 'approved'`,
		scenarioID))
}

func (s *Store) ListApprovedDDSVersions(ctx context.Context, tx pgx.Tx, targetService string) ([]content.ScenarioVersionRecord, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+scenarioVersionColumns+`
		FROM scenario_versions
		WHERE status = 'approved' AND exercise_type = 'dds_processing'
		  AND scenario_id IN (
		      SELECT id FROM scenarios
		      WHERE status <> 'archived' AND ($1::text IS NULL OR target_service = $1)
		  )
		ORDER BY created_at, id
	`, nullableString(targetService))
	if err != nil {
		return nil, content.ErrStorage
	}
	defer rows.Close()
	var out []content.ScenarioVersionRecord
	for rows.Next() {
		v, err := scanScenarioVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, content.ErrStorage
	}
	return out, nil
}

func (s *Store) ListVersions(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID) ([]content.VersionSummary, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, version, status, digest, difficulty, created_by, created_at, approved_by, approved_at
		FROM scenario_versions WHERE scenario_id = $1 ORDER BY version DESC
	`, scenarioID)
	if err != nil {
		return nil, content.ErrStorage
	}
	defer rows.Close()
	var versions []content.VersionSummary
	for rows.Next() {
		var v content.VersionSummary
		var digest []byte
		if err := rows.Scan(&v.ID, &v.Version, &v.Status, &digest, &v.Difficulty, &v.CreatedBy, &v.CreatedAt, &v.ApprovedBy, &v.ApprovedAt); err != nil {
			return nil, content.ErrStorage
		}
		if len(digest) != len(v.Digest) {
			return nil, content.ErrStorage
		}
		copy(v.Digest[:], digest)
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, content.ErrStorage
	}
	return versions, nil
}

func (s *Store) InsertScenarioVersion(ctx context.Context, tx pgx.Tx, v content.ScenarioVersionRecord) (content.ScenarioVersionRecord, error) {
	// v.BodyJSON (content.ScenarioVersionRecord's doc comment) is written
	// verbatim rather than re-marshaling v.Body: v.Body has no omitempty,
	// so an optional field a file legitimately omits would come back as
	// an explicit null, and the stored body would stop reproducing v.Digest.
	bodyJSON := v.BodyJSON
	// approved_by repeats v.CreatedBy as its own $11 (not a second use of
	// $10) — PostgreSQL's parameter type inference otherwise reports
	// "inconsistent types deduced" when the same placeholder backs both a
	// plain INSERT value and a CASE branch, even though both targets are
	// uuid.
	row := tx.QueryRow(ctx, `
		INSERT INTO scenario_versions (id, scenario_id, version, status, body, digest, difficulty, source_task_id, prompt_ref, created_by, approved_by, approved_at, exercise_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
		        CASE WHEN $4 = 'approved' THEN $11::uuid ELSE NULL END,
		        CASE WHEN $4 = 'approved' THEN clock_timestamp() ELSE NULL END, $12)
		RETURNING created_at, approved_by, approved_at
	`, v.ID, v.ScenarioID, v.Version, v.Status, bodyJSON, v.Digest[:], v.Difficulty, v.SourceTaskID, v.PromptRef, v.CreatedBy, v.CreatedBy, v.Body.ExerciseType)
	if err := row.Scan(&v.CreatedAt, &v.ApprovedBy, &v.ApprovedAt); err != nil {
		return content.ScenarioVersionRecord{}, content.ErrStorage
	}
	return v, nil
}

func (s *Store) SupersedeApprovedVersion(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE scenario_versions SET status = 'superseded' WHERE scenario_id = $1 AND status = 'approved'`, scenarioID)
	if err != nil {
		return false, content.ErrStorage
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) SupersedeVersion(ctx context.Context, tx pgx.Tx, versionID uuid.UUID) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE scenario_versions SET status = 'superseded' WHERE id = $1 AND status IN ('draft', 'approved')`, versionID)
	if err != nil {
		return false, content.ErrStorage
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) ApproveVersion(ctx context.Context, tx pgx.Tx, versionID uuid.UUID, approverID uuid.UUID) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE scenario_versions SET status = 'approved', approved_by = $2, approved_at = clock_timestamp()
		WHERE id = $1 AND status = 'draft'
	`, versionID, approverID)
	if err != nil {
		return false, content.ErrStorage
	}
	return tag.RowsAffected() > 0, nil
}

// ------------------------------------------------------------- audit

func (s *Store) AuditRecord(ctx context.Context, tx pgx.Tx, entry audit.Entry) error {
	return audit.Record(ctx, tx, entry)
}
