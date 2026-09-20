package dds

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"
)

// Evidence assembles the immutable close-time snapshot (RFC-001 §6/§7.4,
// evidence.schema.json) for one closed item. item.CloseReason must
// already be set — the application service applies the close Decision to
// its in-memory Item before calling Evidence, the same order it applies
// any other accepted Decision.
func (exercise) Evidence(item training.Item, actions []training.Action, events []training.ItemEvent, cutoffLogSeq int64, closedAt time.Time) (training.Evidence, error) {
	if item.CloseReason == nil {
		return training.Evidence{}, fmt.Errorf("dds: evidence requires a closed item, got state %q", item.State)
	}

	comments := buildEvidenceComments(actions, cutoffLogSeq)
	interruptions := item.Interruptions
	if interruptions == nil {
		interruptions = []training.Interruption{}
	}
	interruption := buildEvidenceInterruption(*item.CloseReason, closedAt, events)
	body := training.EvidenceBody{
		Schema:            "emsim/evidence/v1",
		ItemID:            item.ID,
		RunID:             item.RunID,
		LessonID:          item.LessonID,
		TraineeID:         item.UserID,
		WorkstationNo:     item.WorkstationNo,
		ScenarioVersionID: item.ScenarioVersionID,
		ScenarioDigest:    item.ScenarioDigest,
		TargetService:     item.TargetService,
		Timing: training.EvidenceTiming{
			OpenS:          item.TimingEffective.OpenS,
			PrimaryS:       item.TimingEffective.PrimaryS,
			CompleteS:      item.TimingEffective.CompleteS,
			OpenAnchor:     "offered_at",
			PrimaryAnchor:  "offered_at",
			CompleteAnchor: "primary_at",
		},
		OfferedAt:     item.OfferedAt,
		OpenedAt:      item.OpenedAt,
		ClosedAt:      closedAt,
		CloseReason:   *item.CloseReason,
		FinalReaction: item.Reaction,
		Mode:          item.Mode,
		FinalCard:     item.Card,
		Actions:       buildEvidenceActions(actions, cutoffLogSeq),
		Events:        buildEvidenceEvents(events),
		Calls:         []any{},
		Comments:      comments,
		Derived:       buildEvidenceDerived(item, actions, cutoffLogSeq, closedAt, len(comments)),
		CutoffLogSeq:  cutoffLogSeq,
		PrimaryAt:     item.PrimaryAt,
		Deadlines: training.EvidenceDeadlines{
			OpenAt:     item.Deadlines.OpenAt,
			PrimaryAt:  item.Deadlines.PrimaryAt,
			CompleteAt: item.Deadlines.CompleteAt,
		},
		Interruption:  interruption,
		ExerciseType:  content.ExerciseTypeDDSProcessing,
		Interruptions: interruptions,
	}
	return training.SealEvidence(body)
}

// buildEvidenceInterruption fills evidence.schema.json's singular
// "interruption" — distinct from "interruptions" (server-restart
// markers, C6) — only for a stop-triggered close (close_reason=
// interrupted, RFC-001 §7.5's lesson.close worker, C8). unreached_events
// lists the keys of this item's own events the worker cancelled rather
// than delivered (SkipReasonLessonStopped) — events that were already
// terminal (delivered, or skipped for an unrelated reason) before stop
// happened are not "unreached".
func buildEvidenceInterruption(closeReason training.CloseReason, closedAt time.Time, events []training.ItemEvent) *training.EvidenceInterruption {
	if closeReason != training.CloseInterrupted {
		return nil
	}
	var unreached []string
	for _, e := range events {
		if e.State == training.EventSkipped && e.SkipReason != nil && *e.SkipReason == training.SkipReasonLessonStopped {
			unreached = append(unreached, e.EventKey)
		}
	}
	return &training.EvidenceInterruption{Reason: "stop", StoppedAt: closedAt, UnreachedEvents: unreached}
}

// buildEvidenceEvents projects the item's own item_events rows into
// evidence.schema.json's simplified per-event shape. The caller
// (application service) is documented to cancel any still-scheduled
// event before calling Evidence, so every entry here is normally
// delivered or skipped; a lingering "scheduled" one is preserved as-is
// rather than hidden, since evidence must reflect the actual database
// state at cutoff, not a re-derived one.
func buildEvidenceEvents(events []training.ItemEvent) []training.EvidenceEvent {
	out := make([]training.EvidenceEvent, 0, len(events))
	for _, e := range events {
		out = append(out, training.EvidenceEvent{
			Key: e.EventKey, State: e.State, AnchorAt: e.AnchorAt, DueAt: e.DueAt,
			DeliveredAt: e.DeliveredAt, Late: e.Late, SkipReason: e.SkipReason,
		})
	}
	return out
}

// buildEvidenceActions copies actions up to cutoffLogSeq into the
// snapshot's own action-log shape. The caller (application service) is
// documented to already pass exactly [1, cutoffLogSeq] sorted by LogSeq;
// the LogSeq guard here is a cheap defensive check, not a substitute for
// that contract.
func buildEvidenceActions(actions []training.Action, cutoffLogSeq int64) []training.EvidenceAction {
	out := make([]training.EvidenceAction, 0, len(actions))
	for _, a := range actions {
		if a.LogSeq > cutoffLogSeq {
			continue
		}
		entry := training.EvidenceAction{
			Seq:      a.Seq,
			Type:     a.Type,
			Accepted: a.Accepted,
			ServerAt: a.ServerAt,
			ActionID: a.ID,
			LogSeq:   a.LogSeq,
		}
		if len(a.Payload) > 0 {
			entry.Payload = a.Payload
		}
		if !a.Accepted {
			rejection := a.Rejection
			entry.Rejection = &rejection
		}
		if a.Effect != nil {
			entry.Effect = a.Effect
		}
		if a.ReactionAfter != "" {
			reactionAfter := a.ReactionAfter
			entry.ReactionAfter = &reactionAfter
		}
		out = append(out, entry)
	}
	return out
}

// buildEvidenceComments extracts add_comment/set_status(comment) text
// from every accepted action up to cutoffLogSeq, in the order they were
// applied — evidence.schema.json's own description: "дублируют payload
// действий — для удобства judge". Rejected attempts are not included: a
// rejected set_status never became part of the card's actual history.
func buildEvidenceComments(actions []training.Action, cutoffLogSeq int64) []training.EvidenceComment {
	var out []training.EvidenceComment
	for _, a := range actions {
		if a.LogSeq > cutoffLogSeq || !a.Accepted {
			continue
		}
		switch a.Type {
		case training.CommandAddComment:
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(a.Payload, &payload); err != nil || strings.TrimSpace(payload.Text) == "" {
				continue
			}
			out = append(out, training.EvidenceComment{Seq: a.Seq, Text: payload.Text})
		case training.CommandSetStatus:
			var payload struct {
				Status  content.Reaction `json:"status"`
				Comment string           `json:"comment"`
			}
			if err := json.Unmarshal(a.Payload, &payload); err != nil || strings.TrimSpace(payload.Comment) == "" {
				continue
			}
			status := payload.Status
			out = append(out, training.EvidenceComment{Seq: a.Seq, Text: payload.Comment, WithStatus: &status})
		}
	}
	return out
}

// buildEvidenceDerived computes the actual server-measured intervals
// (RFC-001 §7.2/§8) — no pause compensation, since slice 3 never marks
// an interruption. open/primary/work seconds are nil exactly when their
// anchor timestamp (opened_at/primary_at) never happened, matching
// evidence.schema.json's own ["number","null"] typing for those three
// fields; total_seconds is always present.
func buildEvidenceDerived(item training.Item, actions []training.Action, cutoffLogSeq int64, closedAt time.Time, commentCount int) training.EvidenceDerived {
	derived := training.EvidenceDerived{
		TotalSeconds: closedAt.Sub(item.OfferedAt).Seconds(),
		CommentCount: commentCount,
	}
	if item.OpenedAt != nil {
		seconds := item.OpenedAt.Sub(item.OfferedAt).Seconds()
		derived.OpenSeconds = &seconds
	}
	if item.PrimaryAt != nil {
		primarySeconds := item.PrimaryAt.Sub(item.OfferedAt).Seconds()
		derived.PrimarySeconds = &primarySeconds
		workSeconds := closedAt.Sub(*item.PrimaryAt).Seconds()
		derived.WorkSeconds = &workSeconds
	}

	var chain []content.Reaction
	var rejectedTransitions int
	var primaryStatus *content.Reaction
	for _, a := range actions {
		if a.LogSeq > cutoffLogSeq || a.Type != training.CommandSetStatus {
			continue
		}
		if !a.Accepted {
			rejectedTransitions++
			continue
		}
		var payload struct {
			Status content.Reaction `json:"status"`
		}
		if err := json.Unmarshal(a.Payload, &payload); err != nil {
			continue
		}
		chain = append(chain, payload.Status)
		if primaryStatus == nil {
			status := payload.Status
			primaryStatus = &status
		}
	}
	derived.Chain = chain
	derived.RejectedTransitions = rejectedTransitions
	derived.PrimaryStatus = primaryStatus
	return derived
}
