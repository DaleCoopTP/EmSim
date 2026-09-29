package reporting

import (
	"context"
	"emsim/internal/content"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"emsim/internal/platform/tasks"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const KindBuild tasks.Kind = "report.build"

type ArtifactStore interface {
	Store
	WithTx(context.Context, func(pgx.Tx) error) error
	InsertReportFile(context.Context, pgx.Tx, ReportFile) error
	ListReportFiles(context.Context, uuid.UUID) ([]ReportFile, error)
	ReportFileByID(context.Context, uuid.UUID) (ReportFile, error)
	InsertBlob(context.Context, pgx.Tx, Blob) (Blob, error)
	MarkReportReady(context.Context, pgx.Tx, uuid.UUID, Blob, time.Time) error
}

type Service struct {
	store ArtifactStore
	tasks *tasks.Store
}

func NewService(store ArtifactStore, taskStore *tasks.Store) *Service {
	return &Service{store: store, tasks: taskStore}
}

func (s *Service) LessonReport(ctx context.Context, lessonID uuid.UUID) (LessonReport, error) {
	return s.store.LessonReport(ctx, lessonID)
}
func (s *Service) Results(ctx context.Context, userID uuid.UUID) ([]ItemResult, error) {
	return s.store.Results(ctx, userID)
}
func (s *Service) Progress(ctx context.Context, userID uuid.UUID) (Progress, error) {
	return s.store.Progress(ctx, userID)
}
func (s *Service) ResultsFor(ctx context.Context, userID uuid.UUID, exerciseType content.ExerciseType) ([]ItemResult, error) {
	return s.store.ResultsFor(ctx, userID, exerciseType)
}
func (s *Service) ProgressFor(ctx context.Context, userID uuid.UUID, exerciseType content.ExerciseType) (Progress, error) {
	return s.store.ProgressFor(ctx, userID, exerciseType)
}

type buildPayload struct {
	ReportFileID uuid.UUID `json:"report_file_id"`
	LessonID     uuid.UUID `json:"lesson_id"`
	RequestedBy  uuid.UUID `json:"requested_by"`
	BasisDigest  string    `json:"basis_digest"`
}

func (s *Service) RequestPDF(ctx context.Context, lessonID, actorID uuid.UUID) (ReportFile, error) {
	report, err := s.store.LessonReport(ctx, lessonID)
	if err != nil {
		return ReportFile{}, err
	}
	now := time.Now().UTC()
	_, basis, digest, err := NewSnapshot(report, now)
	if err != nil {
		return ReportFile{}, err
	}
	file := ReportFile{ID: uuid.New(), LessonID: lessonID, TaskID: uuid.New(), RequestedBy: actorID, Basis: basis, BasisDigest: digest, Status: ReportQueued, RequestedAt: now}
	payload, err := json.Marshal(buildPayload{ReportFileID: file.ID, LessonID: lessonID, RequestedBy: actorID, BasisDigest: hex.EncodeToString(digest[:])})
	if err != nil {
		return ReportFile{}, err
	}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.store.InsertReportFile(ctx, tx, file); err != nil {
			return err
		}
		_, _, err := s.tasks.EnqueueTx(ctx, tx, tasks.EnqueueRequest{TaskID: file.TaskID, Kind: KindBuild, ScopeType: "lesson", ScopeID: &lessonID, DedupKey: "report.build:" + file.ID.String(), Payload: payload, NextAttemptAt: now})
		return err
	})
	return file, err
}

func (s *Service) ListPDFs(ctx context.Context, lessonID uuid.UUID) ([]ReportFile, error) {
	return s.store.ListReportFiles(ctx, lessonID)
}
func (s *Service) ReportFile(ctx context.Context, id uuid.UUID) (ReportFile, error) {
	return s.store.ReportFileByID(ctx, id)
}

func (s *Service) Usage(ctx context.Context, from, to time.Time) (Usage, error) {
	from, to, err := NormalizeUsagePeriod(from, to)
	if err != nil {
		return Usage{}, err
	}
	us, ok := s.store.(UsageStore)
	if !ok {
		return Usage{}, errors.New("reporting: store has no usage statistics")
	}
	return us.Usage(ctx, from, to)
}
