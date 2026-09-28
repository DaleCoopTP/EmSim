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
	// CardStatus (ДДС-3/ADR-032) is ADR-030's derived DDS card status —
	// set only for a dds_processing row, "" for operator112_intake (it
	// has no equivalent notion). See DDSCardStatusOf's own doc comment
	// for the report-time simplification of training/dds.CardStatusOf
	// this reuses. Lives on ItemResult (not just ReportItem) so /my/
	// results carries it too, the same way item_state already does.
	CardStatus CardStatus `json:"card_status,omitempty"`
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
	// IntakeBlocks/IntakePenaltyTotal (112-6/ADR-026) are set only for an
	// operator112_intake row with a final ready assessment on rubric-v2 —
	// nil/empty for every DDS row and for a 112 row still pending/
	// needs_review/on rubric-v1. Read directly from the final assessment's
	// own criteria jsonb (weight/score/penalty_points are already frozen
	// there per revision), not re-derived from "today's" rubric.
	IntakeBlocks       []IntakeBlockScore `json:"intake_blocks,omitempty"`
	IntakePenaltyTotal *float64           `json:"intake_penalty_total,omitempty"`
	// CommentErrors (ДДС-4/ADR-034) is the number of spelling/grammar/
	// punctuation mistakes the judge found in the trainee's comments —
	// set only for a final ready assessment whose rubric has an
	// evaluated G_GRAMMAR (dds/rubric-v3); nil for every other row
	// (rubric-v1/v2, 112, not_applicable or unavailable grammar). Read
	// from the final assessment's own criteria details, so an expert
	// revision that keeps the details keeps the count.
	CommentErrors *int `json:"comment_errors,omitempty"`
}

// CardStatus mirrors training/dds.CardStatus's own values (openapi's
// ItemSummary.card_status enum, ADR-030) — duplicated here rather than
// imported, since reporting only ever reads training's own data through
// lesson_report_rows, never its domain package (RFC-001 §4.2's module
// boundary: a module reads another's rows, not its code).
type CardStatus string

const (
	CardRegistered   CardStatus = "registered"
	CardNotNotified  CardStatus = "not_notified"
	CardInProgress   CardStatus = "in_progress"
	CardRefused      CardStatus = "refused"
	CardCompleted    CardStatus = "completed"
	CardNotCompleted CardStatus = "not_completed"
)

// DDSCardStatusOf is training/dds.CardStatusOf's own report-time
// simplification (ДДС-3/ADR-032): a report row only ever describes an
// already-closed or interrupted item (LessonReport requires lessons.
// state='finished'), so there is no "now" to compare against a primary
// deadline — an item that never received a primary decision at all is
// reported as CardRegistered rather than distinguishing CardNotNotified,
// which needs that live deadline check. reaction/closeReason/itemState
// are lesson_report_rows' own items.reaction/close_reason/state columns.
func DDSCardStatusOf(reaction string, closeReason *string, itemState string) CardStatus {
	switch reaction {
	case "completed", "completed_without_team":
		return CardCompleted
	case "not_accepted", "refused":
		return CardRefused
	}
	if closeReason != nil && *closeReason == "pilot_completed" {
		return CardCompleted
	}
	switch itemState {
	case "interrupted":
		return CardNotCompleted
	case "closed":
		return CardNotCompleted
	}
	switch reaction {
	case "added", "received":
		return CardRegistered
	}
	return CardInProgress
}

// IntakeBlockScore is one operator112/rubric-v2 block's own points-out-
// of-weight breakdown (112-6/ADR-026) — Points is nil only if the block
// criterion itself was unavailable (112-5b's AI-caller divergence
// carve-out), not merely 0.
type IntakeBlockScore struct {
	CriterionID string   `json:"criterion_id"`
	Label       string   `json:"label"`
	Points      *float64 `json:"points"`
	MaxPoints   float64  `json:"max_points"`
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
