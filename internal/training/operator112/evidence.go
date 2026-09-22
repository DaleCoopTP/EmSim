package operator112

import (
	"time"

	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
)

// EvidenceBody is a separate immutable close-time projection. In particular
// it includes what was actually sent, rather than only the final draft.
type EvidenceBody struct {
	Schema            string                    `json:"schema"`
	ExerciseType      content.ExerciseType      `json:"exercise_type"`
	ItemID            uuid.UUID                 `json:"item_id"`
	RunID             uuid.UUID                 `json:"run_id"`
	LessonID          uuid.UUID                 `json:"lesson_id"`
	TraineeID         uuid.UUID                 `json:"trainee_id"`
	WorkstationNo     int                       `json:"workstation_no"`
	ScenarioVersionID uuid.UUID                 `json:"scenario_version_id"`
	ScenarioDigest    string                    `json:"scenario_digest"`
	OfferedAt         time.Time                 `json:"offered_at"`
	OpenedAt          *time.Time                `json:"opened_at"`
	ClosedAt          time.Time                 `json:"closed_at"`
	CloseReason       training.CloseReason      `json:"close_reason"`
	Mode              training.Mode             `json:"mode"`
	FinalCard         training.IntakeCard       `json:"final_card"`
	IntakeState       training.IntakeState      `json:"intake_state"`
	Dispatch          *training.IntakeDispatch  `json:"dispatch"`
	Actions           []training.EvidenceAction `json:"actions"`
	CutoffLogSeq      int64                     `json:"cutoff_log_seq"`
	Interruptions     []training.Interruption   `json:"interruptions"`
}

func (exercise) Evidence(item training.Item, actions []training.Action, _ []training.ItemEvent, cutoffLogSeq int64, closedAt time.Time) (training.Evidence, error) {
	body := EvidenceBody{Schema: "operator112_intake/v1", ExerciseType: content.ExerciseTypeOperator112Intake,
		ItemID: item.ID, RunID: item.RunID, LessonID: item.LessonID, TraineeID: item.UserID,
		WorkstationNo: item.WorkstationNo, ScenarioVersionID: item.ScenarioVersionID,
		ScenarioDigest: item.ScenarioDigest, OfferedAt: item.OfferedAt, OpenedAt: item.OpenedAt,
		ClosedAt: closedAt, Mode: item.Mode, Dispatch: item.IntakeDispatch,
		CutoffLogSeq: cutoffLogSeq, Interruptions: item.Interruptions,
		Actions: make([]training.EvidenceAction, 0, len(actions))}
	if item.CloseReason != nil {
		body.CloseReason = *item.CloseReason
	}
	if item.IntakeCard != nil {
		body.FinalCard = *item.IntakeCard
	}
	if item.IntakeState != nil {
		body.IntakeState = *item.IntakeState
	}
	for _, a := range actions {
		if a.LogSeq > cutoffLogSeq {
			continue
		}
		var rejection *training.Rejection
		if !a.Accepted {
			r := a.Rejection
			rejection = &r
		}
		body.Actions = append(body.Actions, training.EvidenceAction{Seq: a.Seq, Type: a.Type,
			Payload: a.Payload, Accepted: a.Accepted, Rejection: rejection, Effect: a.Effect,
			ServerAt: a.ServerAt, ActionID: a.ID, LogSeq: a.LogSeq})
	}
	return training.SealIntakeEvidence(body)
}
