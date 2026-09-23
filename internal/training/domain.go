// Package training implements the training module (RFC-001 §4.2,
// slice-planning.md §4): lessons, assignments, runs, items, the
// idempotent command protocol (ADR-004), and the immutable evidence
// snapshot fixed at close (ADR-006, RFC-001 §6/§7.4). Following
// CLAUDE.md's hexagonal boundary, this package and its dds subpackage
// hold domain rules independent of HTTP and PostgreSQL; the application
// service that coordinates transactions and persistence (slice 3's C4)
// lives in a later file in this same package, and infrastructure
// adapters live in postgres/ and http/ subpackages.
//
// ADR-015 isolates exercise-specific process rules (commands, statuses,
// completion, evidence content) behind the Exercise interface declared
// in exercise.go: internal/training/dds is the one implementation this
// slice supports ("dds_processing"); a future operator112 process
// plugs into the same interface without this package changing.
//
// Item/Action/Command/Decision reuse content.Reaction, content.Workflow
// and content.CardPreview directly rather than redeclaring an equivalent
// vocabulary: content already owns the shape a scenario's card and a
// service's workflow are expressed in (ADR-015 §4.2's module table), and
// training never writes to content's own tables, so this is a read of a
// stable shared type, not the kind of cross-module data access CLAUDE.md
// requires a port for.
package training

import (
	"time"

	"emsim/internal/content"

	"github.com/google/uuid"
)

// ItemState is items.state.
type ItemState string

const (
	ItemOffered     ItemState = "offered"
	ItemOpened      ItemState = "opened"
	ItemInProgress  ItemState = "in_progress"
	ItemClosed      ItemState = "closed"
	ItemInterrupted ItemState = "interrupted"
)

// CloseReason is items.close_reason.
type CloseReason string

const (
	CloseCompleted   CloseReason = "completed"
	CloseRefused     CloseReason = "refused"
	CloseInterrupted CloseReason = "interrupted"
	CloseNoContact   CloseReason = "no_contact"
	CloseCallDropped CloseReason = "call_dropped"
	// ClosePilotCompleted is ADR-017's pilot exception: close from
	// accepted for a scenario version with reference.pilot_goal=
	// accept_card. It does not mean the trainee performed correctly.
	ClosePilotCompleted CloseReason = "pilot_completed"
)

// Mode is lessons.mode / runs.mode.
type Mode string

const (
	ModeIntro    Mode = "intro"
	ModeTraining Mode = "training"
)

// CommandType is actions.type / Command.Type. CallStart/CallEnd/
// ControlReport are declared for forward compatibility with the DB CHECK
// and the OpenAPI contract; internal/training/dds does not implement
// them yet (the phone and post-close control_report messaging are
// slices 4/5) and rejects them as transition_not_allowed.
type CommandType string

const (
	CommandOpen              CommandType = "open"
	CommandSetStatus         CommandType = "set_status"
	CommandAddComment        CommandType = "add_comment"
	CommandSetCardField      CommandType = "set_card_field" // ADR-017
	CommandClose             CommandType = "close"
	CommandCallStart         CommandType = "call_start"
	CommandCallEnd           CommandType = "call_end"
	CommandControlReport     CommandType = "control_report" // not implemented until slice 4/5
	CommandAnswerIncoming    CommandType = "answer_incoming"
	CommandEndIncoming       CommandType = "end_incoming"
	CommandSaveIntakeDraft   CommandType = "save_intake_draft"
	CommandDispatchIntake    CommandType = "dispatch_intake"
	CommandCompleteIntake    CommandType = "complete_intake"
	CommandMarkNoContact     CommandType = "mark_no_contact"
	CommandMarkCallDropped   CommandType = "mark_call_dropped"
	CommandAskIntakeQuestion CommandType = "ask_intake_question"
	CommandHoldIncoming      CommandType = "hold_incoming"
	CommandResumeIncoming    CommandType = "resume_incoming"
)

// Deadlines is items.deadlines: absolute server time, frozen once set.
// OpenAt/PrimaryAt are fixed at offer time from TimingEffective;
// CompleteAt is nil until the first applied primary decision sets it
// (RFC-001 §7.2's "Единая timing policy ДДС").
type Deadlines struct {
	OpenAt     time.Time  `json:"open_at"`
	PrimaryAt  time.Time  `json:"primary_at"`
	CompleteAt *time.Time `json:"complete_at,omitempty"`
}

// Timing is lessons.timing / items.timing_effective, frozen at lesson
// start — a case's scenario never overrides it (RFC-001 §7.2).
// SpawnEveryS is accepted by the contract for the hard-level spawn
// interval; slice 3's application service (C4) rejects a lesson that
// sets it, since spawn_card/hard-mode issuance is slice 4.
type Timing struct {
	OpenS       int  `json:"open_s"`
	PrimaryS    int  `json:"primary_s"`
	CompleteS   int  `json:"complete_s"`
	SpawnEveryS *int `json:"spawn_every_s,omitempty"`
}

// Item is the pure-domain view of one items row an Exercise decides
// against, plus enough of its ancestry (Mode, PilotGoal, a Workflow
// snapshot, and the lesson/run/scenario identifiers Evidence embeds)
// that neither Decide nor Evidence needs a second read. The application
// service is responsible for reading the actual row — joined with its
// run, lesson and scenario version — into this shape, and for
// persisting whatever Decision comes back; Item itself never touches the
// database.
type Item struct {
	ID                uuid.UUID
	ExerciseType      content.ExerciseType
	RunID             uuid.UUID
	LessonID          uuid.UUID
	UserID            uuid.UUID // trainee_id, evidence.schema.json's ancestry field
	WorkstationNo     int
	ScenarioVersionID uuid.UUID
	ScenarioDigest    string // hex sha256, scenario_versions.digest
	TargetService     string
	Ordinal           int
	SpawnedFrom       *uuid.UUID
	State             ItemState
	Reaction          content.Reaction
	// Card is the item's own mutable card instance — a copy of the
	// scenario's CardPreview projection taken when the item was offered.
	// set_card_field (ADR-017) is the only command that changes it.
	Card               content.CardPreview
	IntakeCard         *IntakeCard
	IntakeState        *IntakeState
	IntakeScript       []string
	IntakeDialogue     *content.Intake112Dialogue
	AvailableQuestions []IntakeQuestionOption
	IntakeRecipients   []string
	IntakeDispatch     *IntakeDispatch
	// Workflow is the services.workflow snapshot taken at offer time
	// (content.Workflow's shape) so a later edit to the service's
	// workflow cannot retroactively change an already-issued card's
	// rules — the same immutability principle RFC-001 §6 applies to a
	// scenario version's own content.
	Workflow content.Workflow
	// PilotGoal is the reference.pilot_goal snapshot (ADR-017); "" means
	// the ordinary DDS completion rules in dds.decideClose apply.
	PilotGoal        string
	Contacts         []content.Contact
	CallPolicy       content.Call
	Calls            []Call
	Mode             Mode
	Seq              int64
	LogSeq           int64
	StopCutoffLogSeq *int64
	Interruptions    []Interruption
	TimingEffective  Timing
	Deadlines        Deadlines
	OfferedAt        time.Time
	OpenedAt         *time.Time
	// PrimaryAt is the first applied primary decision's server time —
	// nil until set once; a repeat decision never changes it
	// (RFC-001 §7.2: "Повтор статуса не перезапускает таймер").
	PrimaryAt   *time.Time
	ClosedAt    *time.Time
	CloseReason *CloseReason
}

// RecordingState is the persisted state of the manifest declared by a
// completed phone call. Expired is an effective presentation state: the
// immutable manifest remains awaiting in storage until a later slice chooses
// to materialize expiration.
type RecordingState string

const (
	RecordingAbsent   RecordingState = "absent"
	RecordingAwaiting RecordingState = "awaiting"
	RecordingReady    RecordingState = "ready"
	RecordingExpired  RecordingState = "expired"
)

// RecordingManifest is declared atomically by call_end. Its bytes are never
// put into a command or the database; BlobID is populated only after upload.
type RecordingManifest struct {
	SHA256 [32]byte
	Size   int64
	MIME   string
}

// Call is training's durable phone timeline. The row is mutable only while
// completing/uploading the declared recording; evidence copies its close-time
// projection and is immutable.
type Call struct {
	ID                        uuid.UUID
	ItemID                    uuid.UUID
	ContactKey                string
	StartedAt                 time.Time
	EndedAt                   *time.Time
	ReactionAtCall            content.Reaction
	BlobID                    *uuid.UUID
	AcceptedBy                *string
	Summary                   *string
	Recording                 *RecordingManifest
	RecordingState            RecordingState
	RecordingUploadDeadlineAt *time.Time
	RecordingReceivedAt       *time.Time
}

type Blob struct {
	ID        uuid.UUID
	SHA256    [32]byte
	MIME      string
	Size      int64
	CreatedAt time.Time
}

type VoiceAsset struct {
	ID                uuid.UUID
	ScenarioVersionID uuid.UUID
	Key               string
	Voice             string
	BlobID            uuid.UUID
}

// Interruption records a recovery boundary on an item. It is stored as the
// items.interruptions JSON array so later recovery code can append history
// without rewriting immutable evidence.
type Interruption struct {
	RecoveryID uuid.UUID `json:"recovery_id"`
	Cause      string    `json:"cause"`
	DetectedAt time.Time `json:"detected_at"`
}

// EventState is item_events.state.
type EventState string

const (
	EventScheduled EventState = "scheduled"
	EventDelivered EventState = "delivered"
	EventSkipped   EventState = "skipped"
)

// Scenario-event skip reasons item_events.skip_reason takes when the
// application service cancels a still-scheduled event ahead of its
// due_at (RFC-001 §7.4/§7.5's close/stop pseudocode: "отменить
// scheduled" before evidence is assembled). The scheduler's own lazy
// fallback (service.go's tickEvent) reuses SkipReasonItemClosed for any
// event that reaches its due_at after the item already closed by some
// other path.
const (
	SkipReasonItemClosed    = "item_closed"
	SkipReasonLessonStopped = "lesson_stopped"
	// SkipReasonSpawnPlanMismatch is tickEvent's own terminal outcome for
	// a spawn_card event whose target no longer matches the run's queue
	// cursor at due_at — the hard scheduler's next_offer_at tick can
	// legitimately consume the same queue slot first (ADR-018 only
	// guarantees the *static* plan is reachable, not that runtime
	// issuance order matches it). Retrying such an event can never
	// succeed, so it is skipped rather than left scheduled forever.
	SkipReasonSpawnPlanMismatch = "spawn_plan_mismatch"
)

// ItemEvent is one scheduled or terminal scenario event for an item.
type ItemEvent struct {
	ID          uuid.UUID
	ItemID      uuid.UUID
	EventKey    string
	AnchorAt    time.Time
	DueAt       time.Time
	State       EventState
	DeliveredAt *time.Time
	Late        bool
	SkipReason  *string
}

// ControlReport is an append-only post-close controller message. ActionID
// ties it to the idempotent command that created it.
type ControlReport struct {
	ID        uuid.UUID
	ItemID    uuid.UUID
	ActionID  uuid.UUID
	Text      string
	CreatedAt time.Time
}

// Rejection is one of the fixed reasons a command is not applied. The
// stale_seq/lesson_stopped/item_closed group is checked generically by
// the application service before Exercise.Decide is ever called (they
// apply to any exercise type, per ADR-015); Decide only ever returns
// TransitionNotAllowed, CommentRequired or InvalidPayload. This is a
// plain string type, not httpapi.ErrorCode, because domain rules do not
// import the HTTP layer (CLAUDE.md); the application/HTTP layers map
// these values onto Receipt.error_code and an HTTP status.
type Rejection string

const (
	RejectStaleSeq             Rejection = "stale_seq"
	RejectLessonStopped        Rejection = "lesson_stopped"
	RejectItemClosed           Rejection = "item_closed"
	RejectTransitionNotAllowed Rejection = "transition_not_allowed"
	RejectCommentRequired      Rejection = "comment_required"
	// RejectInvalidPayload is a structurally well-formed JSON body whose
	// content a domain rule still refuses — e.g. set_card_field naming a
	// path other than the one ADR-017 allows, or an empty value. Nothing
	// upstream validates a command's payload against the OpenAPI schema
	// at runtime, so this check is the domain's own responsibility.
	RejectInvalidPayload Rejection = "invalid_payload"
	RejectCallRequired   Rejection = "call_required"
	RejectCallInProgress Rejection = "call_in_progress"
	RejectCallNotActive  Rejection = "call_not_active"
)

// HTTPStatus is the fixed status a new (non-replay) decision carries,
// per ADR-004/RFC-001 §5's three-way split for POST /items/{id}/actions.
// A replay of an existing command_id instead reuses the ORIGINAL
// decision's status, which the application service reads back from the
// stored action row rather than recomputing here.
func (r Rejection) HTTPStatus() int {
	switch r {
	case RejectStaleSeq, RejectLessonStopped, RejectItemClosed:
		return 409
	default: // transition_not_allowed, comment_required, invalid_payload
		return 422
	}
}
