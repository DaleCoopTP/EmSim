// Package reporting owns read-only lesson reports, trainee history and
// immutable PDF snapshot data. It never changes training or assessment rows.
package reporting

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("reporting: not found")

type ReportStatus string

const (
	ReportQueued   ReportStatus = "queued"
	ReportBuilding ReportStatus = "building"
	ReportReady    ReportStatus = "ready"
	ReportFailed   ReportStatus = "failed"
)

type Blob struct {
	ID     uuid.UUID
	SHA256 [32]byte
	MIME   string
	Size   int64
}

type ReportFile struct {
	ID          uuid.UUID       `json:"id"`
	LessonID    uuid.UUID       `json:"lesson_id"`
	TaskID      uuid.UUID       `json:"task_id"`
	RequestedBy uuid.UUID       `json:"-"`
	Basis       json.RawMessage `json:"-"`
	BasisDigest [32]byte        `json:"-"`
	Blob        *Blob           `json:"-"`
	Status      ReportStatus    `json:"status"`
	RequestedAt time.Time       `json:"requested_at"`
	GeneratedAt *time.Time      `json:"generated_at"`
	DownloadURL *string         `json:"download_url"`
}

type AssessmentStatus string

const (
	AssessmentReady       AssessmentStatus = "ready"
	AssessmentNeedsReview AssessmentStatus = "needs_review"
	AssessmentUnavailable AssessmentStatus = "unavailable"
	AssessmentPending     AssessmentStatus = "pending"
	AssessmentNotAssessed AssessmentStatus = "not_assessed"
)

type PublicError struct {
	CriterionID string  `json:"criterion_id"`
	Status      string  `json:"status"`
	Label       string  `json:"label"`
	GuideRef    *string `json:"guide_ref"`
}

type ItemResult struct {
	ItemID           uuid.UUID        `json:"item_id"`
	LessonID         uuid.UUID        `json:"lesson_id"`
	LessonTitle      string           `json:"lesson_title"`
	CardNumber       string           `json:"card_number"`
	Difficulty       int              `json:"difficulty"`
	ClosedAt         time.Time        `json:"closed_at"`
	ItemState        string           `json:"item_state"`
	AssessmentStatus AssessmentStatus `json:"assessment_status"`
	AssessmentKind   *string          `json:"assessment_kind"`
	Score            *float64         `json:"score"`
	Passed           *bool            `json:"passed"`
	OpenSeconds      *float64         `json:"open_seconds"`
	WorkSeconds      *float64         `json:"work_seconds"`
	TotalSeconds     *float64         `json:"total_seconds"`
	LevelAtStart     string           `json:"level_at_start"`
	CriticalErrors   []string         `json:"critical_errors"`
	Errors           []PublicError    `json:"errors"`
	Interruptions    any              `json:"interruptions"`
}

type ReportItem struct {
	ItemResult
	UserID             uuid.UUID  `json:"user_id"`
	FullName           string     `json:"full_name"`
	WorkstationNo      int        `json:"workstation_no"`
	ScenarioTitle      string     `json:"scenario_title"`
	Ordinal            int        `json:"ordinal"`
	Level              string     `json:"level"`
	AssessmentID       *uuid.UUID `json:"assessment_id"`
	AssessmentRevision *int       `json:"assessment_revision"`
}

type Participant struct {
	UserID             uuid.UUID `json:"user_id"`
	FullName           string    `json:"full_name"`
	WorkstationNo      int       `json:"workstation_no"`
	Level              string    `json:"level"`
	Items              int       `json:"items"`
	ReadyAssessments   int       `json:"ready_assessments"`
	PendingAssessments int       `json:"pending_assessments"`
	InterruptedItems   int       `json:"interrupted_items"`
	AvgScore           *float64  `json:"avg_score"`
	AvgOpenSeconds     *float64  `json:"avg_open_seconds"`
	AvgWorkSeconds     *float64  `json:"avg_work_seconds"`
	AvgTotalSeconds    *float64  `json:"avg_total_seconds"`
}

type ErrorFrequency struct {
	CriterionID string `json:"criterion_id"`
	Count       int    `json:"count"`
}

type HistogramBucket struct {
	Bucket string `json:"bucket"`
	Count  int    `json:"count"`
}

type Aggregates struct {
	AvgScore           *float64          `json:"avg_score"`
	ReadyAssessments   int               `json:"ready_assessments"`
	PendingAssessments int               `json:"pending_assessments"`
	InterruptedItems   int               `json:"interrupted_items"`
	ScoreHistogram     []HistogramBucket `json:"score_histogram"`
	TopErrors          []ErrorFrequency  `json:"top_errors"`
}

type Lesson struct {
	ID         uuid.UUID `json:"id"`
	Title      string    `json:"title"`
	Mode       string    `json:"mode"`
	FinishedAt time.Time `json:"finished_at"`
}

type LessonReport struct {
	Lesson       Lesson        `json:"lesson"`
	Items        []ReportItem  `json:"items"`
	Participants []Participant `json:"participants"`
	Aggregates   Aggregates    `json:"aggregates"`
}

type ProgressLesson struct {
	LessonID uuid.UUID `json:"lesson_id"`
	Title    string    `json:"title"`
	Date     time.Time `json:"date"`
	Items    int       `json:"items"`
	AvgScore *float64  `json:"avg_score"`
}

type Progress struct {
	UserID             uuid.UUID        `json:"user_id"`
	Level              string           `json:"level"`
	ItemsTotal         int              `json:"items_total"`
	CompletedItems     int              `json:"completed_items"`
	PendingAssessments int              `json:"pending_assessments"`
	AvgScore           *float64         `json:"avg_score"`
	ByLesson           []ProgressLesson `json:"by_lesson"`
	ErrorFrequency     []ErrorFrequency `json:"error_frequency"`
}

// Snapshot is the entire immutable source of a PDF, including the display
// values and final assessment provenance inside ReportItem.
type Snapshot struct {
	Version    int          `json:"version"`
	CapturedAt time.Time    `json:"captured_at"`
	Report     LessonReport `json:"report"`
}

func NewSnapshot(report LessonReport, capturedAt time.Time) (Snapshot, []byte, [32]byte, error) {
	snapshot := Snapshot{Version: 1, CapturedAt: capturedAt.UTC(), Report: report}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return Snapshot{}, nil, [32]byte{}, err
	}
	return snapshot, body, sha256.Sum256(body), nil
}
