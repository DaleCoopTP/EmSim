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
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
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
	case training.CommandCallStart:
		return decideCallStart(item, cmd, now), nil
	case training.CommandCallEnd:
		return decideCallEnd(item, cmd), nil
	case training.CommandAnswerIncoming:
		return decideAnswerIncoming(item, cmd, now), nil
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
	if item.CallPolicy.Required && item.CallPolicy.BeforeStatus == payload.Status && !requiredCallFinished(item) {
		return rejectDecision(item, training.RejectCallRequired)
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
	// ADR-030: under a workflow with terminal statuses, saving one closes
	// the card in the same command — the same preconditions close has.
	if item.Workflow.IsTerminal(payload.Status) {
		if rejection, ok := closePrecondition(item); !ok {
			return rejectDecision(item, rejection)
		}
		reason := closeReasonFor(payload.Status)
		decision.State = training.ItemClosed
		decision.Close = &reason
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
	if statusClosesCard(item) || item.State == training.ItemOffered {
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
	if statusClosesCard(item) {
		// ADR-030: a DDS dispatcher never edits the 112 card.
		return rejectDecision(item, training.RejectTransitionNotAllowed)
	}
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
	if statusClosesCard(item) {
		// ADR-030: the terminal status itself closed the card; there is
		// no separate close step under such a workflow.
		return rejectDecision(item, training.RejectTransitionNotAllowed)
	}
	if rejection, ok := closePrecondition(item); !ok {
		return rejectDecision(item, rejection)
	}
	var reason training.CloseReason
	switch item.Reaction {
	case content.ReactionNotAccepted, content.ReactionRefused, content.ReactionCompleted, content.ReactionCompletedWithoutTeam:
		reason = closeReasonFor(item.Reaction)
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

// statusClosesCard reports ADR-030's mode: the card's workflow snapshot
// names terminal statuses, so saving one of them closes the card, and
// close/add_comment/set_card_field have no place. A workflow without
// terminal statuses is a slice 2–7 pilot service (ADR-017) and keeps
// the old commands.
func statusClosesCard(item training.Item) bool {
	return len(item.Workflow.Terminal) > 0
}

// closePrecondition is what any close — explicit or by a terminal
// status — requires: no call in progress and the required call, if any,
// finished (RFC-001 §7.4).
func closePrecondition(item training.Item) (training.Rejection, bool) {
	if activeCall(item) != nil {
		return training.RejectCallInProgress, false
	}
	if item.CallPolicy.Required && !requiredCallFinished(item) {
		return training.RejectCallRequired, false
	}
	return "", true
}

// closeReasonFor maps a closing reaction to items.close_reason:
// not_accepted/refused end the card as refused, completed and 103's
// completed_without_team as completed.
func closeReasonFor(status content.Reaction) training.CloseReason {
	switch status {
	case content.ReactionNotAccepted, content.ReactionRefused:
		return training.CloseRefused
	default:
		return training.CloseCompleted
	}
}

type callStartPayload struct {
	Contact string `json:"contact"`
}

func decideCallStart(item training.Item, cmd training.Command, now time.Time) training.Decision {
	if item.State == training.ItemOffered {
		return rejectDecision(item, training.RejectTransitionNotAllowed)
	}
	if activeCall(item) != nil {
		return rejectDecision(item, training.RejectCallInProgress)
	}
	var payload callStartPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil || strings.TrimSpace(payload.Contact) == "" {
		return rejectDecision(item, training.RejectInvalidPayload)
	}
	found := false
	for _, contact := range item.Contacts {
		if contact.Key == payload.Contact {
			found = true
			break
		}
	}
	if !found {
		return rejectDecision(item, training.RejectInvalidPayload)
	}
	return training.Decision{Accepted: true, Reaction: item.Reaction, State: item.State, Card: item.Card,
		StartCall: &training.Call{ContactKey: payload.Contact, StartedAt: now, ReactionAtCall: item.Reaction, RecordingState: training.RecordingAbsent}}
}

type callEndPayload struct {
	CallID     uuid.UUID `json:"call_id"`
	AcceptedBy string    `json:"accepted_by"`
	Summary    string    `json:"summary"`
	Recording  *struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
		MIME   string `json:"mime"`
	} `json:"recording"`
}

func decideCallEnd(item training.Item, cmd training.Command) training.Decision {
	var payload callEndPayload
	if err := json.Unmarshal(cmd.Payload, &payload); err != nil || payload.CallID == uuid.Nil {
		return rejectDecision(item, training.RejectInvalidPayload)
	}
	active := activeCall(item)
	if active == nil || active.ID != payload.CallID {
		return rejectDecision(item, training.RejectCallNotActive)
	}
	// ADR-031: an incoming call carries no call log and no recording; an
	// outgoing one still needs «Кто принял»/«Суть сообщения» (ДДС-3 may
	// relax this).
	if !active.Outgoing() {
		if payload.Recording != nil {
			return rejectDecision(item, training.RejectInvalidPayload)
		}
		return training.Decision{Accepted: true, Reaction: item.Reaction, State: item.State, Card: item.Card,
			EndCall: &training.CallEnd{CallID: payload.CallID, AcceptedBy: strings.TrimSpace(payload.AcceptedBy), Summary: strings.TrimSpace(payload.Summary)}}
	}
	if strings.TrimSpace(payload.AcceptedBy) == "" || strings.TrimSpace(payload.Summary) == "" {
		return rejectDecision(item, training.RejectInvalidPayload)
	}
	var manifest *training.RecordingManifest
	if payload.Recording != nil {
		bytes, err := hex.DecodeString(payload.Recording.SHA256)
		if err != nil || len(bytes) != 32 || payload.Recording.Size < 1 || payload.Recording.Size > 10<<20 || (payload.Recording.MIME != "audio/webm" && payload.Recording.MIME != "audio/ogg" && payload.Recording.MIME != "audio/wav") {
			return rejectDecision(item, training.RejectInvalidPayload)
		}
		manifest = &training.RecordingManifest{Size: payload.Recording.Size, MIME: payload.Recording.MIME}
		copy(manifest.SHA256[:], bytes)
	}
	return training.Decision{Accepted: true, Reaction: item.Reaction, State: item.State, Card: item.Card, EndCall: &training.CallEnd{CallID: payload.CallID, AcceptedBy: strings.TrimSpace(payload.AcceptedBy), Summary: strings.TrimSpace(payload.Summary), Recording: manifest}}
}

func activeCall(item training.Item) *training.Call {
	for i := range item.Calls {
		if item.Calls[i].EndedAt == nil {
			return &item.Calls[i]
		}
	}
	return nil
}
func requiredCallFinished(item training.Item) bool {
	for _, c := range item.Calls {
		// ADR-031: an incoming call from the same contact does not count.
		if c.Outgoing() && c.ContactKey == item.CallPolicy.To && c.EndedAt != nil {
			return true
		}
	}
	return false
}

func reactionListContains(list []content.Reaction, target content.Reaction) bool {
	for _, r := range list {
		if r == target {
			return true
		}
	}
	return false
}
