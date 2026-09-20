// Package dds implements training.Exercise for exercise_type
// "dds_processing" — the one process this slice supports (ADR-015).
// Command handling, transition rules, and evidence content are all
// isolated here, behind training.Exercise: the application service
// (internal/training/service.go, slice 3's C4) is the only caller, and
// it treats Exercise as a black box selected by exercise_type, never
// reaching into this package's own types.
//
// Command coverage in this slice: open, set_status, add_comment,
// set_card_field (ADR-017), close. call_start/call_end/control_report
// are declared by the contract but not implemented until slices 4/5
// (RFC-001 §7.3/§7.5) — Decide rejects them as transition_not_allowed,
// the same rejection an unsupported state transition gets, since from
// the trainee's point of view there is simply nothing to do with them
// yet.
package dds

import (
	"encoding/json"
	"strings"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"
)

// pilotGoalAcceptCard is reference.pilot_goal's one defined value
// (openapi.yaml's ScenarioReference.pilot_goal enum) — ADR-017's pilot
// exception: close is additionally allowed from accepted for a scenario
// version carrying this goal.
const pilotGoalAcceptCard = "accept_card"

// allowedFieldCorrectionPath is the one JSON Pointer set_card_field
// accepts (ADR-017) — a specific, named exception, not a general JSON
// Patch surface. Adding a second path is a new decision, not a config
// change to this constant.
const allowedFieldCorrectionPath = "/card/address/okrug"

// Exercise is the training.Exercise value for "dds_processing". It has
// no fields: every rule below is a pure function of (item, command, now)
// with no state to carry between calls.
var Exercise training.Exercise = exercise{}

type exercise struct{}

func (exercise) Decide(item training.Item, cmd training.Command, now time.Time) (training.Decision, error) {
	switch cmd.Type {
	case training.CommandOpen:
		return decideOpen(item, now), nil
	case training.CommandSetStatus:
		return decideSetStatus(item, cmd, now), nil
	case training.CommandAddComment:
		return decideAddComment(item, cmd), nil
	case training.CommandSetCardField:
		return decideSetCardField(item, cmd), nil
	case training.CommandClose:
		return decideClose(item), nil
	default:
		return rejectDecision(item, training.RejectTransitionNotAllowed), nil
	}
}

// rejectDecision is every rejection's common shape: Decide never reports
// a state/reaction/card change alongside a rejection, so a caller can
// always apply Decision.Reaction/State/Card unconditionally instead of
// branching on Accepted first.
func rejectDecision(item training.Item, rejection training.Rejection) training.Decision {
	return training.Decision{
		Accepted:  false,
		Rejection: rejection,
		Reaction:  item.Reaction,
		State:     item.State,
		Card:      item.Card,
	}
}

// decideOpen is the card's first command: offered -> opened,
// added -> received (RFC-001 §4/slice-planning.md §4's "получение и
// открытие карточки"). A second open (a new command_id after the first
// already applied, not a replay of the same one) finds the item no
// longer offered and is rejected, not silently repeated.
func decideOpen(item training.Item, now time.Time) training.Decision {
	if item.State != training.ItemOffered {
		return rejectDecision(item, training.RejectTransitionNotAllowed)
	}
	openedAt := now
	return training.Decision{
		Accepted: true,
		Reaction: content.ReactionReceived,
		State:    training.ItemOpened,
		Card:     item.Card,
		OpenedAt: &openedAt,
	}
}

type setStatusPayload struct {
	Status  content.Reaction `json:"status"`
	Comment string           `json:"comment"`
}

// decideSetStatus applies one workflow transition: item.Workflow (the
// target service's snapshot taken at offer time) decides which
// reactions are reachable from the current one and which require a
// comment. The first ever applied decision from an item (item.PrimaryAt
// still nil) fixes primary_at and, from it, deadlines.complete_at
// (RFC-001 §7.2's "Единая timing policy ДДС"); a later transition
// (e.g. not_accepted -> accepted) leaves both alone, matching "Повтор
// статуса не перезапускает таймер" — this is not a repeat of the same
// status, but the same rule applies to the timer once primary_at exists.
func decideSetStatus(item training.Item, cmd training.Command, now time.Time) training.Decision {
	if item.State == training.ItemOffered {
		return rejectDecision(item, training.RejectTransitionNotAllowed)
	}
	var payload setStatusPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil || !payload.Status.Valid() {
		return rejectDecision(item, training.RejectInvalidPayload)
	}
	if !reactionListContains(item.Workflow.Transitions[item.Reaction], payload.Status) {
		return rejectDecision(item, training.RejectTransitionNotAllowed)
	}
	if reactionListContains(item.Workflow.CommentRequired, payload.Status) && strings.TrimSpace(payload.Comment) == "" {
		return rejectDecision(item, training.RejectCommentRequired)
	}

	decision := training.Decision{
		Accepted: true,
		Reaction: payload.Status,
		State:    training.ItemInProgress,
		Card:     item.Card,
	}
	if item.PrimaryAt == nil {
		primaryAt := now
		completeAt := now.Add(time.Duration(item.TimingEffective.CompleteS) * time.Second)
		decision.PrimaryAt = &primaryAt
		decision.CompleteAt = &completeAt
	}
	return decision
}

type addCommentPayload struct {
	Text string `json:"text"`
}

// decideAddComment records a free-text comment without changing reaction
// or state; it is only meaningful once the card is open (RFC-001 §7.1's
// workflow check applies here too — a comment before open has no card
// state to attach to).
func decideAddComment(item training.Item, cmd training.Command) training.Decision {
	if item.State == training.ItemOffered {
		return rejectDecision(item, training.RejectTransitionNotAllowed)
	}
	var payload addCommentPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil || strings.TrimSpace(payload.Text) == "" {
		return rejectDecision(item, training.RejectInvalidPayload)
	}
	return training.Decision{
		Accepted: true,
		Reaction: item.Reaction,
		State:    item.State,
		Card:     item.Card,
	}
}

type setCardFieldPayload struct {
	Path  string `json:"path"`
	Value string `json:"value"`
}

// decideSetCardField is ADR-017's pilot field-correction command: the
// one allowed path, a non-empty value, and only while the primary
// decision can still change (received or not_accepted — after accepted
// the card is done being corrected). Correctness of Value against the
// scenario's hidden reference.field_corrections is deliberately not
// checked here: the trainee's job is to notice and fix the error
// themselves, not to be told whether their fix was right.
func decideSetCardField(item training.Item, cmd training.Command) training.Decision {
	var payload setCardFieldPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
		return rejectDecision(item, training.RejectInvalidPayload)
	}
	if payload.Path != allowedFieldCorrectionPath {
		return rejectDecision(item, training.RejectInvalidPayload)
	}
	value := strings.TrimSpace(payload.Value)
	if value == "" {
		return rejectDecision(item, training.RejectInvalidPayload)
	}
	if item.Reaction != content.ReactionReceived && item.Reaction != content.ReactionNotAccepted {
		return rejectDecision(item, training.RejectTransitionNotAllowed)
	}

	oldValue := item.Card.Address.Okrug
	card := item.Card
	card.Address.Okrug = value
	card.Address.Text = formatPilotAddressText(card.Address)

	return training.Decision{
		Accepted: true,
		Reaction: item.Reaction,
		State:    item.State,
		Card:     card,
		Effect: map[string]any{
			"path": payload.Path,
			"old":  oldValue,
			"new":  value,
		},
	}
}

// decideClose implements RFC-001 §7.4/ADR-011's terminal-status close
// rule, extended by ADR-017's pilot exception. set_status never closes
// the card by itself; comment requirements are already enforced when
// item.Reaction reached its current value (decideSetStatus above), so
// close does not re-check them. No call can be active in this slice (the
// phone is slice 5), so that RFC-001 §7.4 precondition has nothing to
// check yet either.
func decideClose(item training.Item) training.Decision {
	var reason training.CloseReason
	switch item.Reaction {
	case content.ReactionNotAccepted, content.ReactionRefused:
		reason = training.CloseRefused
	case content.ReactionCompleted, content.ReactionCompletedWithoutTeam:
		reason = training.CloseCompleted
	case content.ReactionAccepted:
		if item.PilotGoal != pilotGoalAcceptCard {
			return rejectDecision(item, training.RejectTransitionNotAllowed)
		}
		reason = training.ClosePilotCompleted
	default:
		return rejectDecision(item, training.RejectTransitionNotAllowed)
	}
	return training.Decision{
		Accepted: true,
		Reaction: item.Reaction,
		State:    training.ItemClosed,
		Card:     item.Card,
		Close:    &reason,
	}
}

func reactionListContains(list []content.Reaction, target content.Reaction) bool {
	for _, r := range list {
		if r == target {
			return true
		}
	}
	return false
}
