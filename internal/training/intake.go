package training

import (
	"emsim/internal/content"
	"github.com/google/uuid"
	"strings"
	"time"
)

// IntakeField keeps absence of an answer distinct from an explicit unknown
// answer. Negative is reserved for the presence question.
type IntakeField struct {
	State string `json:"state"`
	Value string `json:"value,omitempty"`
}

type IntakeAddress struct {
	Country     IntakeField `json:"country"`
	Region      IntakeField `json:"region"`
	City        IntakeField `json:"city"`
	Object      IntakeField `json:"object"`
	Okrug       IntakeField `json:"okrug"`
	District    IntakeField `json:"district"`
	Street      IntakeField `json:"street"`
	House       IntakeField `json:"house"`
	Building    IntakeField `json:"building"`
	Structure   IntakeField `json:"structure"`
	Flat        IntakeField `json:"flat"`
	Entrance    IntakeField `json:"entrance"`
	Floor       IntakeField `json:"floor"`
	Code        IntakeField `json:"code"`
	Landmark    IntakeField `json:"landmark"`
	Descriptive IntakeField `json:"descriptive"`
}

// IntakeCard is the operator's mutable draft. Metadata is copied from the
// approved call and never accepted from a command payload.
type IntakeCard struct {
	Number          string                   `json:"number"`
	AON             string                   `json:"aon"`
	CallLocalTime   string                   `json:"call_local_time"`
	CallTimeZone    string                   `json:"call_time_zone"`
	ApplicantName   IntakeField              `json:"applicant_name"`
	ApplicantStatus IntakeField              `json:"applicant_status"`
	Age             IntakeField              `json:"age"`
	Address         IntakeAddress            `json:"address"`
	IncidentType    IntakeField              `json:"incident_type"`
	Complaint       IntakeField              `json:"complaint"`
	VictimsPresent  IntakeField              `json:"victims_present"`
	VictimsCount    IntakeField              `json:"victims_count"`
	ProvidedPhone   IntakeField              `json:"provided_phone"`
	OnSitePhone     IntakeField              `json:"on_site_phone"`
	Channel         IntakeField              `json:"channel"`
	ForeignLanguage IntakeField              `json:"foreign_language"`
	NoOnSite        IntakeField              `json:"no_on_site"`
	NoAccess        IntakeField              `json:"no_access"`
	IncidentTypes   []string                 `json:"incident_types,omitempty"`
	Profiles        map[string]IntakeProfile `json:"profiles,omitempty"`
}

type IntakeProfileAnswer struct {
	State  string   `json:"state"`
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
}

type IntakeProfile struct {
	DefinitionID string                         `json:"definition_id"`
	Version      int                            `json:"version"`
	Answers      map[string]IntakeProfileAnswer `json:"answers"`
}

type IntakeServiceSuggestion struct {
	ServiceCode string   `json:"service_code"`
	Reasons     []string `json:"reasons"`
}

type IntakeServiceReview struct {
	Suggested  []IntakeServiceSuggestion `json:"suggested"`
	Selected   []string                  `json:"selected"`
	Reason     string                    `json:"reason,omitempty"`
	ReviewedAt time.Time                 `json:"reviewed_at"`
}

func UnansweredIntakeCard(number, aon, localTime, zone string) IntakeCard {
	u := IntakeField{State: "unanswered"}
	return IntakeCard{Number: number, AON: aon, CallLocalTime: localTime, CallTimeZone: zone,
		ApplicantName: u, ApplicantStatus: u, Age: u, IncidentType: u, Complaint: u,
		VictimsPresent: u, VictimsCount: u, ProvidedPhone: u, OnSitePhone: u,
		Channel: u, ForeignLanguage: u, NoOnSite: u, NoAccess: u,
		Address: IntakeAddress{Country: u, Region: u, City: u, Object: u,
			Okrug: u, District: u, Street: u, House: u, Building: u,
			Structure: u, Flat: u, Entrance: u, Floor: u, Code: u,
			Landmark: u, Descriptive: u}}
}

// ValidIntakeCard validates only the form's representation, not correctness
// against the scenario's private reference. Incomplete cards may be saved.
func ValidIntakeCard(card IntakeCard) bool {
	required := []IntakeField{card.ApplicantName, card.ApplicantStatus, card.Age,
		card.Address.City, card.Address.Street, card.Address.House, card.Address.Building,
		card.Address.Flat, card.Address.Landmark, card.IncidentType,
		card.VictimsCount, card.ProvidedPhone}
	for _, field := range required {
		if !validIntakeField(field, 1000, false) {
			return false
		}
	}
	optional := []IntakeField{card.Address.Country, card.Address.Region, card.Address.Object,
		card.Address.Okrug, card.Address.District, card.Address.Structure,
		card.Address.Entrance, card.Address.Floor, card.Address.Code,
		card.Address.Descriptive, card.OnSitePhone}
	for _, field := range optional {
		if !validIntakeField(field, 1000, true) {
			return false
		}
	}
	return validIntakeField(card.Complaint, 1999, false) &&
		validVictimsPresent(card.VictimsPresent) &&
		validIntakeField(card.Channel, 100, true) &&
		validOptionalChoice(card.ForeignLanguage, "yes") &&
		validOptionalChoice(card.NoOnSite, "yes") &&
		validOptionalChoice(card.NoAccess, "yes")
}

func validOptionalChoice(field IntakeField, choice string) bool {
	return validIntakeField(field, 1000, true) &&
		(field.State != "known" || field.Value == choice)
}

func validIntakeField(field IntakeField, maxLength int, legacyOptional bool) bool {
	if len(field.Value) > maxLength || strings.TrimSpace(field.Value) != field.Value {
		return false
	}
	switch field.State {
	case "known":
		return field.Value != ""
	case "unanswered", "unknown":
		return field.Value == ""
	case "":
		return legacyOptional && field.Value == ""
	default:
		return false
	}
}

func validVictimsPresent(field IntakeField) bool {
	if field.State == "negative" {
		return field.Value == ""
	}
	return field.State == "known" && field.Value == "yes" ||
		(field.State == "unanswered" || field.State == "unknown") && field.Value == ""
}

type IntakeLine struct {
	ID         string    `json:"id,omitempty"`
	SourceID   string    `json:"source_id,omitempty"`
	Speaker    string    `json:"speaker,omitempty"`
	CallID     string    `json:"call_id,omitempty"`
	CommandID  string    `json:"command_id,omitempty"`
	QuestionID string    `json:"question_id,omitempty"`
	TopicID    string    `json:"topic_id,omitempty"`
	Reveals    []string  `json:"reveals,omitempty"`
	Text       string    `json:"text"`
	ServerAt   time.Time `json:"server_at"`
}

type IntakeQuestionOption struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	TopicID string `json:"topic_id"`
	Asked   bool   `json:"asked"`
}

// IntakeCallerTurn is one round of 112-5a/ADR-024's free-text caller
// chat: send_caller_message starts a turn as CallerTurnPending and the
// application service enqueues caller.reply for it in the same
// transaction; a worker resolves it to CallerTurnAnswered (appending the
// applicant's reply to Transcript) or, once the task's own attempt
// budget is exhausted, CallerTurnFailed via the registered Finalizer.
// hold_incoming/end_incoming/mark_call_dropped/complete_intake cancel a
// still-pending turn to CallerTurnCancelled in their own command
// transaction. Turn never renumbers on cancel/failure — it is both
// caller.reply's dedup_key suffix and ApplyCallerReply's "is this still
// the turn I was asked to answer" check, so a late worker attempt for an
// already-resolved turn safely no-ops rather than double-applying.
type IntakeCallerTurn struct {
	Turn           int    `json:"turn"`
	OperatorLineID string `json:"operator_line_id"`
	Status         string `json:"status"` // pending, answered, cancelled, failed
	// Adapter names which CallerReplier produced the reply (e.g.
	// "stub/v1") — set only once Status=answered, so the instructor's
	// review can tell a stub-generated reply from a future model's
	// without a separate server-side setting.
	Adapter string `json:"adapter,omitempty"`
	// Source (112-5b/ADR-025) is one of the CallerTurnSource* constants —
	// set only once Status=answered. Generation is set only when Source
	// is CallerTurnSourceModel or CallerTurnSourceFallback: opening/
	// scripted/stub replies never call a model, so recording generation
	// parameters for them would be meaningless. Both exist so a replayed
	// evaluation (RFC-001 §6 — derived assessment inputs are sealed) can
	// tell exactly how a reply was produced without re-deriving it.
	Source      string                  `json:"source,omitempty"`
	Generation  *IntakeCallerGeneration `json:"generation,omitempty"`
	RequestedAt time.Time               `json:"requested_at"`
	ResolvedAt  *time.Time              `json:"resolved_at,omitempty"`
	// Reason is set for Status=cancelled ("held"/"ended"/"dropped"/
	// "closed") or Status=failed (a short technical cause, never a raw
	// error — RFC-001 §9 forbids operational detail leaking to a
	// trainee-visible field).
	Reason string `json:"reason,omitempty"`
}

// IntakeCallerTurn.Status values.
const (
	CallerTurnPending   = "pending"
	CallerTurnAnswered  = "answered"
	CallerTurnCancelled = "cancelled"
	CallerTurnFailed    = "failed"
)

// IntakeCallerTurn.Source values (112-5b/ADR-025). Opening/Scripted/Stub
// answer without a model call; Model/Fallback do, and are the only two
// that carry a Generation. Which of these a turn gets is decided by the
// CallerReplier implementation (internal/training/operator112), not by
// this package.
const (
	CallerTurnSourceOpening  = "opening"
	CallerTurnSourceScripted = "scripted"
	CallerTurnSourceModel    = "model"
	CallerTurnSourceFallback = "fallback"
	CallerTurnSourceStub     = "stub"
)

// IntakeCallerGeneration records a model-produced (or fallback) caller
// reply's own provenance — RFC-001 §6's "derived assessment inputs are
// also sealed" extended to caller replies, not just assessment_inputs:
// PromptVersion identifies the prompt layout (slice-112-5b-plan.md's
// decision 4), the rest are the request's own generation parameters.
// Regenerating the same turn is not expected to reproduce this exact
// text — it documents what was asked for, not a cache key.
type IntakeCallerGeneration struct {
	Model         string  `json:"model"`
	PromptVersion string  `json:"prompt_version"`
	Temperature   float64 `json:"temperature,omitempty"`
	TopP          float64 `json:"top_p,omitempty"`
	RepeatPenalty float64 `json:"repeat_penalty,omitempty"`
	MaxTokens     int     `json:"max_tokens,omitempty"`
}

type IntakeState struct {
	Mode string `json:"mode,omitempty"`
	// Finale selects the item's completion route: "" (absent, including
	// every item created before ADR-023) keeps the pre-112-4 routes —
	// dispatch_intake for incoming_call, review_service_selection then
	// complete_profile_case for card_only. "notify" is the single
	// "Сохранить → оповестить и сохранить карточку" route ADR-023 adds
	// for card_only and full_case items created from this slice on.
	Finale            string                    `json:"finale,omitempty"`
	Catalog           *content.IntakeCatalog    `json:"catalog,omitempty"`
	InactiveProfiles  map[string]IntakeProfile  `json:"inactive_profiles,omitempty"`
	SuggestedServices []IntakeServiceSuggestion `json:"suggested_services,omitempty"`
	ServiceReview     *IntakeServiceReview      `json:"service_review,omitempty"`
	CallStatus        string                    `json:"call_status"` // ringing, connected, held, ended
	Transcript        []IntakeLine              `json:"transcript"`
	AskedQuestionIDs  []string                  `json:"asked_question_ids,omitempty"`
	// CallerMode is full_case's own applicant behavior (112-5a/ADR-024):
	// "" and content.CallerModePrepared keep 112-2's scripted dialogue
	// (AvailableQuestions/ask_intake_question); content.CallerModeFreeText
	// opens the trainee's caller-chat window instead — send_caller_message
	// and CallerTurns, never AvailableQuestions. Always "" for card_only
	// and incoming_call.
	CallerMode string `json:"caller_mode,omitempty"`
	// CallerTurns is CallerMode=free_text's own round log — see
	// IntakeCallerTurn. Always empty for a prepared-dialogue item.
	CallerTurns     []IntakeCallerTurn `json:"caller_turns,omitempty"`
	HasSavedDraft   bool               `json:"has_saved_draft"`
	Dispatched      bool               `json:"dispatched"`
	SelectedService string             `json:"selected_service,omitempty"`
	// Notified is Finale="notify"'s own completion gate, parallel to
	// Dispatched: set by notify_services, required by complete_intake,
	// and — once true — blocks every further draft/type/service edit.
	Notified   bool       `json:"notified"`
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
}

type IntakeDispatch struct {
	ItemID       uuid.UUID  `json:"item_id"`
	ActionID     uuid.UUID  `json:"action_id"`
	ServiceCode  string     `json:"service_code"`
	CardSnapshot IntakeCard `json:"card_snapshot"`
	SentAt       time.Time  `json:"sent_at"`
}

// IntakeNotificationService is one service in an IntakeNotification's
// list — Suggested distinguishes what the catalog's rules proposed from
// what the operator added by hand (RFC-001's "исходное предложение" is
// state.SuggestedServices at the time of the command, not re-derived
// later from the current draft).
type IntakeNotificationService struct {
	ServiceCode string `json:"service_code"`
	Suggested   bool   `json:"suggested"`
}

// IntakeNotification is ADR-023's single immutable "оповестить и
// сохранить карточку" record — one per item, replacing per-service
// IntakeDispatch rows for card_only and full_case. Unlike IntakeDispatch
// it carries the whole notified service list and, when the operator
// changed it from what the catalog suggested, the required reason.
type IntakeNotification struct {
	ItemID       uuid.UUID                   `json:"item_id"`
	ActionID     uuid.UUID                   `json:"action_id"`
	Services     []IntakeNotificationService `json:"services"`
	Reason       string                      `json:"reason,omitempty"`
	CardSnapshot IntakeCard                  `json:"card_snapshot"`
	NotifiedAt   time.Time                   `json:"notified_at"`
}
