package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"emsim/internal/reporting"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ reporting.Store = (*Store)(nil)

func (s *Store) WithTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) InsertReportFile(ctx context.Context, tx pgx.Tx, file reporting.ReportFile) error {
	_, err := tx.Exec(ctx, `INSERT INTO report_files (id, kind, lesson_id, task_id, requested_by, basis, basis_digest, requested_at) VALUES ($1,'lesson_pdf',$2,$3,$4,$5,$6,$7)`, file.ID, file.LessonID, file.TaskID, file.RequestedBy, file.Basis, file.BasisDigest[:], file.RequestedAt)
	return mapError(err)
}

func (s *Store) ListReportFiles(ctx context.Context, lessonID uuid.UUID) ([]reporting.ReportFile, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.id,r.lesson_id,r.task_id,r.requested_by,r.basis,r.basis_digest,r.blob_id,r.requested_at,r.generated_at,t.status FROM report_files r JOIN tasks t ON t.id=r.task_id WHERE r.lesson_id=$1 ORDER BY r.requested_at DESC`, lessonID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var files []reporting.ReportFile
	for rows.Next() {
		file, err := scanReportFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return files, nil
}

func (s *Store) ReportFileByID(ctx context.Context, id uuid.UUID) (reporting.ReportFile, error) {
	file, err := scanReportFile(s.pool.QueryRow(ctx, `SELECT r.id,r.lesson_id,r.task_id,r.requested_by,r.basis,r.basis_digest,r.blob_id,r.requested_at,r.generated_at,t.status FROM report_files r JOIN tasks t ON t.id=r.task_id WHERE r.id=$1`, id))
	if err != nil {
		return reporting.ReportFile{}, err
	}
	if file.Blob != nil {
		blob, err := s.blobByID(ctx, file.Blob.ID)
		if err != nil {
			return reporting.ReportFile{}, err
		}
		file.Blob = &blob
	}
	return file, nil
}

func (s *Store) InsertBlob(ctx context.Context, tx pgx.Tx, blob reporting.Blob) (reporting.Blob, error) {
	var stored reporting.Blob
	var digest []byte
	err := tx.QueryRow(ctx, `INSERT INTO blobs (id,sha256,mime,size) VALUES ($1,$2,$3,$4) ON CONFLICT (sha256) DO UPDATE SET sha256=EXCLUDED.sha256 RETURNING id,sha256,mime,size`, blob.ID, blob.SHA256[:], blob.MIME, blob.Size).Scan(&stored.ID, &digest, &stored.MIME, &stored.Size)
	copy(stored.SHA256[:], digest)
	return stored, mapError(err)
}

func (s *Store) MarkReportReady(ctx context.Context, tx pgx.Tx, id uuid.UUID, blob reporting.Blob, generatedAt time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE report_files SET blob_id=$2, generated_at=$3 WHERE id=$1 AND blob_id IS NULL`, id, blob.ID, generatedAt)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() != 1 {
		return reporting.ErrNotFound
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scanReportFile(row scanner) (reporting.ReportFile, error) {
	var file reporting.ReportFile
	var digest []byte
	var blobID *uuid.UUID
	var taskStatus string
	if err := row.Scan(&file.ID, &file.LessonID, &file.TaskID, &file.RequestedBy, &file.Basis, &digest, &blobID, &file.RequestedAt, &file.GeneratedAt, &taskStatus); err != nil {
		return reporting.ReportFile{}, mapError(err)
	}
	copy(file.BasisDigest[:], digest)
	if blobID != nil {
		file.Blob = &reporting.Blob{ID: *blobID}
	}
	switch taskStatus {
	case "pending", "waiting":
		file.Status = reporting.ReportQueued
	case "leased":
		file.Status = reporting.ReportBuilding
	case "done":
		if file.Blob != nil {
			file.Status = reporting.ReportReady
		} else {
			file.Status = reporting.ReportFailed
		}
	default:
		file.Status = reporting.ReportFailed
	}
	return file, nil
}

func (s *Store) blobByID(ctx context.Context, id uuid.UUID) (reporting.Blob, error) {
	var blob reporting.Blob
	var digest []byte
	err := s.pool.QueryRow(ctx, `SELECT id,sha256,mime,size FROM blobs WHERE id=$1`, id).Scan(&blob.ID, &digest, &blob.MIME, &blob.Size)
	copy(blob.SHA256[:], digest)
	return blob, mapError(err)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return reporting.ErrNotFound
	}
	return fmt.Errorf("reporting: storage: %w", err)
}

var criterionLabels = map[string]string{
	"T_OPEN": "Скорость открытия карточки", "T_PRIMARY": "Время первичного решения", "T_COMPLETE": "Время обработки",
	"D_PRIMARY": "Первичное решение", "D_COMMENT_REQUIRED": "Обязательный комментарий", "D_FIELD_CORRECTIONS": "Исправление данных",
	"S_SEQUENCE": "Последовательность действий", "C_CALL_MADE": "Обязательный звонок", "C_CALL_LOG": "Оформление звонка",
	"G_ADDRESS": "Корректность адреса", "D_COMMENT_CONTENT": "Содержание комментария", "C_CALL_CONTENT": "Содержание доклада", "C_CALL_LOG_CONTENT": "Содержание журнала звонка", "G_GRAMMAR": "Грамматика",
}

type criterion struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type feedback struct {
	CriterionID string `json:"criterion_id"`
	GuideRef    string `json:"guide_ref"`
}

const rowQuery = `
SELECT lesson_id, lesson_title, lesson_mode, user_id, full_name, workstation_no, level,
       item_id, ordinal, item_state, closed_at, card_number, scenario_title, difficulty,
       open_seconds, work_seconds, total_seconds, interruptions,
       assessment_id, assessment_revision, assessment_kind, assessment_status, score, passed,
       COALESCE(critical_errors, '{}'::text[]), COALESCE(criteria, '[]'::jsonb), COALESCE(feedback, '[]'::jsonb)
FROM lesson_report_rows WHERE lesson_id=$1 ORDER BY full_name, ordinal, item_id`

func (s *Store) LessonReport(ctx context.Context, lessonID uuid.UUID) (reporting.LessonReport, error) {
	var report reporting.LessonReport
	if err := s.pool.QueryRow(ctx, `SELECT id, title, mode, finished_at FROM lessons WHERE id=$1 AND state='finished'`, lessonID).Scan(&report.Lesson.ID, &report.Lesson.Title, &report.Lesson.Mode, &report.Lesson.FinishedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return reporting.LessonReport{}, reporting.ErrNotFound
		}
		return reporting.LessonReport{}, fmt.Errorf("reporting: lesson: %w", err)
	}
	items, err := s.items(ctx, rowQuery, lessonID)
	if err != nil {
		return reporting.LessonReport{}, err
	}
	report.Items = items
	report.Participants, report.Aggregates = reporting.Enrich(items)
	return report, nil
}

func (s *Store) Results(ctx context.Context, userID uuid.UUID) ([]reporting.ItemResult, error) {
	items, err := s.items(ctx, `
SELECT lesson_id, lesson_title, lesson_mode, user_id, full_name, workstation_no, level,
       item_id, ordinal, item_state, closed_at, card_number, scenario_title, difficulty,
       open_seconds, work_seconds, total_seconds, interruptions,
       assessment_id, assessment_revision, assessment_kind, assessment_status, score, passed,
       COALESCE(critical_errors, '{}'::text[]), COALESCE(criteria, '[]'::jsonb), COALESCE(feedback, '[]'::jsonb)
FROM lesson_report_rows WHERE user_id=$1 AND item_state IN ('closed','interrupted') ORDER BY closed_at DESC, item_id`, userID)
	if err != nil {
		return nil, err
	}
	results := make([]reporting.ItemResult, len(items))
	for i := range items {
		results[i] = items[i].ItemResult
	}
	return results, nil
}

func (s *Store) Progress(ctx context.Context, userID uuid.UUID) (reporting.Progress, error) {
	var progress reporting.Progress
	progress.UserID = userID
	if err := s.pool.QueryRow(ctx, `SELECT level FROM users WHERE id=$1`, userID).Scan(&progress.Level); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return reporting.Progress{}, reporting.ErrNotFound
		}
		return reporting.Progress{}, err
	}
	results, err := s.Results(ctx, userID)
	if err != nil {
		return reporting.Progress{}, err
	}
	progress.ItemsTotal, progress.CompletedItems = len(results), len(results)
	var scores []float64
	errorsByID := map[string]int{}
	lessons := map[uuid.UUID]*reporting.ProgressLesson{}
	for _, result := range results {
		p := lessons[result.LessonID]
		if p == nil {
			p = &reporting.ProgressLesson{LessonID: result.LessonID, Title: result.LessonTitle, Date: result.ClosedAt}
			lessons[result.LessonID] = p
		}
		p.Items++
		if result.AssessmentStatus == reporting.AssessmentReady && result.Score != nil {
			scores = append(scores, *result.Score)
			for _, e := range result.Errors {
				errorsByID[e.CriterionID]++
			}
		} else if result.AssessmentStatus != reporting.AssessmentNotAssessed {
			progress.PendingAssessments++
		}
	}
	progress.AvgScore = reporting.Average(scores)
	for _, lesson := range lessons {
		var local []float64
		for _, r := range results {
			if r.LessonID == lesson.LessonID && r.AssessmentStatus == reporting.AssessmentReady && r.Score != nil {
				local = append(local, *r.Score)
			}
		}
		lesson.AvgScore = reporting.Average(local)
		progress.ByLesson = append(progress.ByLesson, *lesson)
	}
	sort.Slice(progress.ByLesson, func(i, j int) bool { return progress.ByLesson[i].Date.After(progress.ByLesson[j].Date) })
	for id, count := range errorsByID {
		progress.ErrorFrequency = append(progress.ErrorFrequency, reporting.ErrorFrequency{CriterionID: id, Count: count})
	}
	sort.Slice(progress.ErrorFrequency, func(i, j int) bool {
		if progress.ErrorFrequency[i].Count != progress.ErrorFrequency[j].Count {
			return progress.ErrorFrequency[i].Count > progress.ErrorFrequency[j].Count
		}
		return progress.ErrorFrequency[i].CriterionID < progress.ErrorFrequency[j].CriterionID
	})
	return progress, nil
}

func (s *Store) items(ctx context.Context, query string, id uuid.UUID) ([]reporting.ReportItem, error) {
	rows, err := s.pool.Query(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("reporting: rows: %w", err)
	}
	defer rows.Close()
	var result []reporting.ReportItem
	for rows.Next() {
		var item reporting.ReportItem
		var lessonMode string
		var criteriaRaw, feedbackRaw []byte
		var critical []string
		var interruptions []byte
		if err := rows.Scan(&item.LessonID, &item.LessonTitle, &lessonMode, &item.UserID, &item.FullName, &item.WorkstationNo, &item.Level, &item.ItemID, &item.Ordinal, &item.ItemState, &item.ClosedAt, &item.CardNumber, &item.ScenarioTitle, &item.Difficulty, &item.OpenSeconds, &item.WorkSeconds, &item.TotalSeconds, &interruptions, &item.AssessmentID, &item.AssessmentRevision, &item.AssessmentKind, &item.AssessmentStatus, &item.Score, &item.Passed, &critical, &criteriaRaw, &feedbackRaw); err != nil {
			return nil, fmt.Errorf("reporting: scan row: %w", err)
		}
		item.LevelAtStart, item.CriticalErrors, item.Interruptions = item.Level, critical, json.RawMessage(interruptions)
		if lessonMode == "intro" {
			item.AssessmentStatus = reporting.AssessmentNotAssessed
			item.AssessmentKind, item.Score, item.Passed = nil, nil, nil
		} else if item.AssessmentStatus == "" {
			item.AssessmentStatus = reporting.AssessmentPending
		}
		item.Errors = publicErrors(criteriaRaw, feedbackRaw, item.AssessmentStatus)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func publicErrors(criteriaRaw, feedbackRaw []byte, status reporting.AssessmentStatus) []reporting.PublicError {
	if status != reporting.AssessmentReady {
		return []reporting.PublicError{}
	}
	var criteria []criterion
	var feedbacks []feedback
	_ = json.Unmarshal(criteriaRaw, &criteria)
	_ = json.Unmarshal(feedbackRaw, &feedbacks)
	guides := map[string]string{}
	for _, f := range feedbacks {
		if f.GuideRef != "" {
			guides[f.CriterionID] = f.GuideRef
		}
	}
	result := make([]reporting.PublicError, 0)
	for _, c := range criteria {
		if c.Status != "partial" && c.Status != "not_met" {
			continue
		}
		label, ok := criterionLabels[c.ID]
		if !ok {
			continue
		}
		var guide *string
		if value, ok := guides[c.ID]; ok {
			guide = &value
		}
		result = append(result, reporting.PublicError{CriterionID: c.ID, Status: c.Status, Label: label, GuideRef: guide})
	}
	return result
}
