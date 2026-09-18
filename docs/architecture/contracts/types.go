// Package architecturecontracts is a design sketch, not production code.
// Implementations must enforce the invariants documented in ../04-contracts.md.
package architecturecontracts

import (
	"context"
	"encoding/json"
	"time"
)

type ID string
type Digest [32]byte
type Version uint64
type Epoch uint64
type RunMode string

const (
	CallCard     RunMode = "call_card"
	CardWorkflow RunMode = "card_workflow"
)

type Scope struct {
	LessonID ID
	RunID    ID
	ItemID   ID
}

type Principal struct {
	UserID    ID
	SessionID ID
}

type Command struct {
	ID               ID              `json:"command_id"`
	ItemID           ID              `json:"item_id"`
	ExpectedVersion  Version         `json:"expected_version"`
	InteractionEpoch Epoch           `json:"interaction_epoch"`
	ClientSequence   uint64          `json:"client_seq"`
	ClientOccurredAt time.Time       `json:"client_occurred_at"`
	Type             string          `json:"type"`
	Payload          json.RawMessage `json:"payload"`
}

type Receipt struct {
	CommandID    ID
	Outcome      string // applied | rejected | recovered_only
	RunVersion   Version
	LastEventSeq uint64
	ServerTime   time.Time
	Replayed     bool
	ErrorCode    string
}

type TimingPolicy struct {
	PrimaryResponseLimit time.Duration
	CardFillLimit        time.Duration
	DialogueLimit        time.Duration
	HardStopAfter        time.Duration
	PrimaryAnchor        string // card_dispatched_to_service
	OutagePolicy         string // review_timing_when_unverifiable
}

type Snapshot struct {
	ID                ID
	Digest            Digest
	ScenarioVersion   ID
	RubricVersion     ID
	ClassifierVersion ID
	WorkflowVersion   ID
	Timing            TimingPolicy
	PrivateFacts      json.RawMessage // never serialize to an operator DTO
	ModelManifest     json.RawMessage
}

type SourcePolicy struct {
	Kind              string // system | student | mixed
	Categories        []ID
	Difficulty        []string
	ServiceProfileID  ID
	SystemWeight      uint32
	StudentWeight     uint32
	ExhaustionPolicy  string // stop | reshuffle
	FrozenPoolVersion ID
	Seed              [32]byte
}

type TurnInput struct {
	Scope             Scope
	TurnID            ID
	LessonEpoch       Epoch
	RunEpoch          Epoch
	ConversationEpoch Epoch
	Snapshot          Snapshot
	Messages          []Message
	AllowedFactIDs    []ID
	InputDigest       Digest
}

type Message struct {
	ID          ID
	Speaker     string
	Text        string
	ServerAt    time.Time
	UtteranceID ID
}

type TurnOutput struct {
	TurnID          ID
	Text            string
	RevealedFactIDs []ID
	StopReason      string
	OutputDigest    Digest
}

type CallerSimulator interface {
	GenerateTurn(context.Context, TurnInput) (TurnOutput, error)
}

type Lease struct {
	TaskID    ID
	ScopeID   ID
	Kind      string
	WorkerID  string
	Token     uint64
	Attempt   uint32
	ExpiresAt time.Time
}

type TaskSpec struct {
	Kind                string
	ScopeID             ID
	InputRef            ID
	InputDigest         Digest
	DedupKey            string
	ExpectedRunEpoch    Epoch
	ExpectedLessonEpoch Epoch
	NotBefore           time.Time
}

// TaskStore owns lease management only. Effect+terminal commits use specialized
// domain committers below, in one DB transaction; never separate Save and Done.
type TaskStore interface {
	Claim(context.Context, string, string) (Lease, bool, error)
	Heartbeat(context.Context, Lease) (time.Time, error)
}

type TurnCommit struct {
	Lease                     Lease
	Scope                     Scope
	ExpectedLessonEpoch       Epoch
	ExpectedRunEpoch          Epoch
	ExpectedConversationEpoch Epoch
	Output                    TurnOutput
}

type TurnCommitter interface {
	// Checks current owner, database-clock expiry, lesson/run barriers and turn ID.
	// Saves message, disclosure, task terminal state, audit and outbox atomically.
	CommitTurn(context.Context, TurnCommit) error
}

type CommandService interface {
	// Authorizes first, then returns a saved receipt or executes a single transaction.
	Apply(context.Context, Principal, Scope, Command) (Receipt, error)
}

type StopCommand struct {
	CommandID       ID
	LessonID        ID
	RunID           ID // optional: stop just this run
	ExpectedVersion Version
	Reason          string
	Mode            string // finish | stop | technical_abort
}

type StopService interface {
	Stop(context.Context, Principal, StopCommand) (Receipt, error)
}

type EvidenceRef struct {
	ID             ID
	RunID          ID
	ItemID         ID // optional for run-level manifest
	Digest         Digest
	SnapshotDigest Digest
	CutoffSequence uint64
	Schema         string
	Coverage       string
}

type CriterionResult struct {
	CriterionID ID
	Status      string // met | not_met | partial | not_applicable | unavailable
	Score       *float64
	Weight      float64
	EvidenceIDs []ID
	Explanation string
}

type AssessmentResult struct {
	AssessmentID    ID
	Revision        Version
	Evidence        EvidenceRef
	RubricDigest    Digest
	EvaluatorDigest Digest
	Status          string
	Criteria        []CriterionResult
	Score           *float64 // absent when not yet assessable
	Verdict         *string
}

type EvidenceReader interface {
	ReadVerified(context.Context, EvidenceRef) (json.RawMessage, error)
}

type DeterministicEvaluator interface {
	Evaluate(context.Context, EvidenceRef, json.RawMessage) ([]CriterionResult, error)
}

type SemanticJudge interface {
	Evaluate(context.Context, EvidenceRef, json.RawMessage) ([]CriterionResult, error)
}

type AssessmentCommitter interface {
	// Unique revision, lease fencing, immutable result and outbox are one commit.
	Commit(context.Context, Lease, AssessmentResult) error
}

type MediaBinding struct {
	MediaSessionID         ID
	Scope                  Scope
	PlaybackEpoch          Epoch
	AuthorizationExpiresAt time.Time
}

type MediaControl interface {
	Connect(context.Context, MediaBinding) error
	Play(context.Context, MediaBinding, ID, string) error // stable playback ID
	Stop(context.Context, MediaBinding) error
}

type Event struct {
	ID               ID
	Type             string
	AggregateID      ID
	AggregateVersion Version
	OccurredAt       time.Time
	CorrelationID    ID
	CausationID      ID
	Payload          json.RawMessage
}
