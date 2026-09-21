// Package postgres is the pgx-backed adapter for internal/assessment's
// own Store port — the same shape internal/training/postgres and
// internal/content/postgres already use: every method but WithTx takes
// an explicit pgx.Tx, the caller owns commit/rollback, and errors this
// package cannot say more about come back as assessment.ErrNotFound or a
// wrapped storage error.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"emsim/internal/assessment"
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

// *Store structurally satisfies assessment.Store — asserted here so a
// divergence between the two fails the build at the adapter, matching
// internal/training/postgres.Store's own assertion.
var _ assessment.Store = (*Store)(nil)

func (s *Store) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("assessment: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("assessment: commit transaction: %w", err)
	}
	return nil
}

func (s *Store) AuditRecord(ctx context.Context, tx pgx.Tx, entry audit.Entry) error {
	return audit.Record(ctx, tx, entry)
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return assessment.ErrNotFound
	}
	return fmt.Errorf("assessment: storage: %w", err)
}

// ------------------------------------------------------------ assessment_inputs

func (s *Store) InsertInput(ctx context.Context, tx pgx.Tx, itemID uuid.UUID, canonicalBody []byte, digest [32]byte) (uuid.UUID, error) {
	id := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO assessment_inputs (id, item_id, body, digest) VALUES ($1, $2, $3, $4)`,
		id, itemID, canonicalBody, digest[:]); err != nil {
		return uuid.Nil, mapErr(err)
	}
	return id, nil
}

func (s *Store) InputByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (uuid.UUID, assessment.InputBody, bool, error) {
	var id uuid.UUID
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT id, body FROM assessment_inputs WHERE item_id = $1`, itemID).Scan(&id, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, assessment.InputBody{}, false, nil
	}
	if err != nil {
		return uuid.Nil, assessment.InputBody{}, false, mapErr(err)
	}
	var body assessment.InputBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return uuid.Nil, assessment.InputBody{}, false, fmt.Errorf("assessment: decode input body: %w", err)
	}
	return id, body, true, nil
}

func (s *Store) InputByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (assessment.InputBody, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT body FROM assessment_inputs WHERE id = $1`, id).Scan(&raw); err != nil {
		return assessment.InputBody{}, mapErr(err)
	}
	var body assessment.InputBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return assessment.InputBody{}, fmt.Errorf("assessment: decode input body: %w", err)
	}
	return body, nil
}

// ------------------------------------------------------------ assessments

// criterionRow is CriterionResult's jsonb shape (openapi.yaml's
// CriterionResult), kept separate from the domain type since
// CriterionResult has no json tags of its own — assessment's domain
// package stays free of encoding concerns.
type criterionRow struct {
	ID           string   `json:"id"`
	Status       string   `json:"status"`
	Score        *float64 `json:"score"`
	Weight       float64  `json:"weight"`
	Critical     bool     `json:"critical"`
	EvidenceRefs []string `json:"evidence_refs"`
	Explanation  string   `json:"explanation"`
}

func toCriterionRow(r assessment.CriterionResult) criterionRow {
	refs := r.EvidenceRefs
	if refs == nil {
		refs = []string{}
	}
	return criterionRow{
		ID: r.ID, Status: string(r.Status), Score: r.Score, Weight: r.Weight,
		Critical: r.Critical, EvidenceRefs: refs, Explanation: r.Explanation,
	}
}

func toCriteriaJSON(results []assessment.CriterionResult) ([]byte, error) {
	rows := make([]criterionRow, 0, len(results))
	for _, r := range results {
		rows = append(rows, toCriterionRow(r))
	}
	return json.Marshal(rows)
}

func fromCriteriaJSON(raw []byte) ([]assessment.CriterionResult, error) {
	var rows []criterionRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	results := make([]assessment.CriterionResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, assessment.CriterionResult{
			ID: row.ID, Status: assessment.CriterionStatus(row.Status), Score: row.Score, Weight: row.Weight,
			Critical: row.Critical, EvidenceRefs: row.EvidenceRefs, Explanation: row.Explanation,
		})
	}
	return results, nil
}

type feedbackRow struct {
	CriterionID string `json:"criterion_id"`
	Severity    string `json:"severity"`
	Text        string `json:"text"`
	GuideRef    string `json:"guide_ref,omitempty"`
}

func toFeedbackJSON(feedback []assessment.Feedback) ([]byte, error) {
	rows := make([]feedbackRow, 0, len(feedback))
	for _, f := range feedback {
		rows = append(rows, feedbackRow{CriterionID: f.CriterionID, Severity: f.Severity, Text: f.Text, GuideRef: f.GuideRef})
	}
	return json.Marshal(rows)
}

func fromFeedbackJSON(raw []byte) ([]assessment.Feedback, error) {
	var rows []feedbackRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	feedback := make([]assessment.Feedback, 0, len(rows))
	for _, row := range rows {
		feedback = append(feedback, assessment.Feedback{CriterionID: row.CriterionID, Severity: row.Severity, Text: row.Text, GuideRef: row.GuideRef})
	}
	return feedback, nil
}

const assessmentColumns = `id, item_id, revision, kind, status, evidence_digest, input_id, source_task_id, base_revision, rubric_version, rubric_effective, score, passed, criteria, critical_errors, feedback, model, created_by, reason, created_at`

func scanAssessment(row pgx.Row) (assessment.Assessment, error) {
	var a assessment.Assessment
	var digest []byte
	var rubricRaw []byte
	var criteriaRaw []byte
	var feedbackRaw []byte
	var criticalErrors []string
	if err := row.Scan(
		&a.ID, &a.ItemID, &a.Revision, &a.Kind, &a.Status, &digest, &a.InputID, &a.SourceTaskID, &a.BaseRevision,
		&a.RubricVersion, &rubricRaw, &a.Score, &a.Passed, &criteriaRaw, &criticalErrors, &feedbackRaw,
		&a.Model, &a.CreatedBy, &a.Reason, &a.CreatedAt,
	); err != nil {
		return assessment.Assessment{}, err
	}
	copy(a.EvidenceDigest[:], digest)
	if err := json.Unmarshal(rubricRaw, &a.RubricEffective); err != nil {
		return assessment.Assessment{}, fmt.Errorf("assessment: decode rubric_effective: %w", err)
	}
	criteria, err := fromCriteriaJSON(criteriaRaw)
	if err != nil {
		return assessment.Assessment{}, fmt.Errorf("assessment: decode criteria: %w", err)
	}
	a.Criteria = criteria
	feedback, err := fromFeedbackJSON(feedbackRaw)
	if err != nil {
		return assessment.Assessment{}, fmt.Errorf("assessment: decode feedback: %w", err)
	}
	a.Feedback = feedback
	a.CriticalErrors = criticalErrors
	return a, nil
}

func (s *Store) InsertAssessment(ctx context.Context, tx pgx.Tx, a assessment.Assessment) (uuid.UUID, error) {
	id := a.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	rubricJSON, err := json.Marshal(a.RubricEffective)
	if err != nil {
		return uuid.Nil, fmt.Errorf("assessment: marshal rubric_effective: %w", err)
	}
	criteriaJSON, err := toCriteriaJSON(a.Criteria)
	if err != nil {
		return uuid.Nil, fmt.Errorf("assessment: marshal criteria: %w", err)
	}
	feedbackJSON, err := toFeedbackJSON(a.Feedback)
	if err != nil {
		return uuid.Nil, fmt.Errorf("assessment: marshal feedback: %w", err)
	}
	criticalErrors := a.CriticalErrors
	if criticalErrors == nil {
		criticalErrors = []string{}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO assessments (
			id, item_id, revision, kind, status, evidence_digest, input_id, source_task_id, base_revision,
			rubric_version, rubric_effective, score, passed, criteria, critical_errors, feedback,
			model, created_by, reason
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
	`, id, a.ItemID, a.Revision, string(a.Kind), string(a.Status), a.EvidenceDigest[:], a.InputID, a.SourceTaskID,
		a.BaseRevision, a.RubricVersion, rubricJSON, a.Score, a.Passed, criteriaJSON, criticalErrors, feedbackJSON,
		a.Model, a.CreatedBy, a.Reason)
	if err != nil {
		return uuid.Nil, mapErr(err)
	}
	return id, nil
}

func (s *Store) AutoByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (assessment.Assessment, bool, error) {
	row := tx.QueryRow(ctx, `SELECT `+assessmentColumns+` FROM assessments WHERE item_id = $1 AND kind = 'auto'`, itemID)
	a, err := scanAssessment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return assessment.Assessment{}, false, nil
	}
	if err != nil {
		return assessment.Assessment{}, false, mapErr(err)
	}
	return a, true, nil
}

func (s *Store) HasExpert(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (bool, error) {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assessments WHERE item_id = $1 AND kind = 'expert')`, itemID).Scan(&exists); err != nil {
		return false, mapErr(err)
	}
	return exists, nil
}

func (s *Store) FinalByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) (assessment.Assessment, bool, error) {
	row := tx.QueryRow(ctx, `
		SELECT `+assessmentColumns+`
		FROM assessments
		WHERE id = (SELECT assessment_id FROM item_final_assessment WHERE item_id = $1)
	`, itemID)
	a, err := scanAssessment(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return assessment.Assessment{}, false, nil
	}
	if err != nil {
		return assessment.Assessment{}, false, mapErr(err)
	}
	return a, true, nil
}

func (s *Store) RevisionsByItem(ctx context.Context, tx pgx.Tx, itemID uuid.UUID) ([]assessment.Assessment, error) {
	rows, err := tx.Query(ctx, `SELECT `+assessmentColumns+` FROM assessments WHERE item_id = $1 ORDER BY revision`, itemID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]assessment.Assessment, 0)
	for rows.Next() {
		a, err := scanAssessment(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

func (s *Store) ClosedItemsByLesson(ctx context.Context, tx pgx.Tx, lessonID uuid.UUID) ([]assessment.LessonAssessmentItem, error) {
	rows, err := tx.Query(ctx, `
		SELECT i.id, u.id, u.login, u.full_name, u.service_code, u.level, u.active,
		       w.number, i.ordinal, COALESCE(i.card->>'number', ''),
		       i.state, i.close_reason, i.closed_at
		FROM items i
		JOIN runs r ON r.id = i.run_id
		JOIN workstations w ON w.id = r.workstation_id
		JOIN users u ON u.id = r.user_id
		WHERE r.lesson_id = $1 AND i.state IN ('closed', 'interrupted')
		ORDER BY w.number, i.ordinal
	`, lessonID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]assessment.LessonAssessmentItem, 0)
	for rows.Next() {
		var row assessment.LessonAssessmentItem
		if err := rows.Scan(&row.ItemID, &row.UserID, &row.Login, &row.FullName, &row.ServiceCode, &row.Level, &row.Active,
			&row.WorkstationNo, &row.Ordinal, &row.CardNumber, &row.ItemState, &row.CloseReason, &row.ClosedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

// ------------------------------------------------------------ trainee_assessment_state

func (s *Store) LockTraineeState(ctx context.Context, tx pgx.Tx, userID uuid.UUID, exerciseType content.ExerciseType) (assessment.TraineeAssessmentState, error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO trainee_assessment_state (user_id, exercise_type, version)
		VALUES ($1, $2, 0)
		ON CONFLICT (user_id, exercise_type) DO NOTHING
	`, userID, string(exerciseType)); err != nil {
		return assessment.TraineeAssessmentState{}, mapErr(err)
	}
	var version int64
	if err := tx.QueryRow(ctx, `
		SELECT version FROM trainee_assessment_state WHERE user_id = $1 AND exercise_type = $2 FOR UPDATE
	`, userID, string(exerciseType)).Scan(&version); err != nil {
		return assessment.TraineeAssessmentState{}, mapErr(err)
	}
	return assessment.TraineeAssessmentState{UserID: userID, ExerciseType: string(exerciseType), Version: version}, nil
}

func (s *Store) BumpTraineeStateVersion(ctx context.Context, tx pgx.Tx, userID uuid.UUID, exerciseType content.ExerciseType) error {
	command, err := tx.Exec(ctx, `
		UPDATE trainee_assessment_state SET version = version + 1 WHERE user_id = $1 AND exercise_type = $2
	`, userID, string(exerciseType))
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("assessment: bump trainee state: no row locked for user %s", userID)
	}
	return nil
}

// ------------------------------------------------------------ training_examples

func (s *Store) InsertTrainingExample(ctx context.Context, tx pgx.Tx, assessmentID uuid.UUID, criterionID string, auto, expert assessment.CriterionResult) error {
	autoJSON, err := json.Marshal(toCriterionRow(auto))
	if err != nil {
		return fmt.Errorf("assessment: marshal auto criterion result: %w", err)
	}
	expertJSON, err := json.Marshal(toCriterionRow(expert))
	if err != nil {
		return fmt.Errorf("assessment: marshal expert criterion result: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO training_examples (id, assessment_id, criterion_id, auto_result, expert_result)
		VALUES ($1, $2, $3, $4, $5)
	`, uuid.New(), assessmentID, criterionID, autoJSON, expertJSON); err != nil {
		return mapErr(err)
	}
	return nil
}
