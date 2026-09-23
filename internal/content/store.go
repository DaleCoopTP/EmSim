package content

import (
	"context"
	"errors"
	"time"

	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrNotFound and ErrStorage mirror internal/auth's pair: every Store
	// method (internal/content/postgres.Store) returns ErrNotFound for a
	// lookup that matched no row, ErrStorage for anything else that went
	// wrong at the database — both live here, not in the postgres
	// package, so Service can check for them without importing its own
	// adapter.
	ErrNotFound = errors.New("not found")
	ErrStorage  = errors.New("storage failure")

	// ErrConflict is Service's import-time rejection of a re-import that
	// disagrees with what is already stored: a known service/classifier
	// code whose stored definition differs from the file's, or a
	// scenario key whose title/target_service/origin the file tries to
	// change, or a (key, version) pair the file re-defines with
	// different content. Always wrapped by a *ConflictError.
	ErrConflict = errors.New("import conflict")
)

// ConflictError names what already-stored thing a re-import disagreed
// with and why.
type ConflictError struct {
	Subject string // e.g. "service:dds_district", "scenario:pilot-tree-01", "scenario:pilot-tree-01@2"
	Reason  string // short machine code: "definition_changed", "title_changed", "target_service_changed", "origin_changed", "version_gap", "version_regression", "content_changed"
}

func (e *ConflictError) Error() string {
	return "import conflict: " + e.Subject + ": " + e.Reason
}

func (e *ConflictError) Unwrap() error { return ErrConflict }

func conflict(subject, reason string) error {
	return &ConflictError{Subject: subject, Reason: reason}
}

// ServiceRecord is one services row — the target of Validate's Catalog
// lookup and what Service.ImportServices upserts. It appears in
// notification_list/target_service, never in trainee-facing output.
type ServiceRecord struct {
	Code     string
	Name     string
	Workflow Workflow
	Active   bool
}

// ClassifierType is one classifier_types row — the incident taxonomy a
// scenario's card.incident.type_code/type_name must agree with.
type ClassifierType struct {
	ID         uuid.UUID
	Code       string
	Name       string
	Features   map[string]any
	Notify     []string
	SourceRow  *int
	ImportedAt time.Time
}

// ScenarioRecord is one scenarios row — the scenario as an identity
// (title, target service, current difficulty/status) independent of any
// particular version's content.
type ScenarioRecord struct {
	ID            uuid.UUID
	Title         string
	TargetService string
	Difficulty    int
	Origin        string
	TicketID      *uuid.UUID
	Status        string
	SourceKey     *string
	CreatedBy     uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ScenarioVersionRecord is one scenario_versions row.
type ScenarioVersionRecord struct {
	ID         uuid.UUID
	ScenarioID uuid.UUID
	Version    int
	Status     string
	Body       Body
	// BodyJSON is the exact canonical JSON bytes Digest was computed
	// from. InsertScenarioVersion (postgres/store.go) writes these bytes
	// to scenario_versions.body verbatim instead of re-marshaling Body:
	// Body has no omitempty, so a field a file legitimately omits
	// (hints, generation, reference.scoring, ...) would otherwise
	// round-trip into an explicit null that matches neither Digest nor
	// scenario.schema.json. Reads (scanScenarioVersion) populate it from
	// the stored column too, so a wire response can serve it as-is.
	BodyJSON     []byte
	Digest       [32]byte
	Difficulty   int
	SourceTaskID *uuid.UUID
	PromptRef    *string
	CreatedBy    uuid.UUID
	CreatedAt    time.Time
	ApprovedBy   *uuid.UUID
	ApprovedAt   *time.Time
}

// ScenarioVersionReference is the small, closed projection semantic
// validation needs for an events[].spawn.(scenario_key,version). Loading only
// these fields avoids decoding another version's full body merely to check
// that it is published and compatible with the referring scenario. Published
// is approval provenance rather than just status: a future cancelled draft
// may be superseded without ever having been approved.
type ScenarioVersionReference struct {
	Status        string
	Published     bool
	ExerciseType  ExerciseType
	TargetService string
}

// ScenarioSummary is the catalogue list row (openapi.yaml ScenarioSummary)
// — a ScenarioRecord joined with its current version's number, for
// GET /scenarios.
type ScenarioSummary struct {
	ScenarioRecord
	ExerciseType ExerciseType
	Version      int
	HasEvents    bool
}

// ScenarioDetail is GET /scenarios/{id} (openapi.yaml Scenario) — a
// summary plus its current approved version's full content.
type ScenarioDetail struct {
	ScenarioSummary
	VersionID uuid.UUID
	Body      Body
	// BodyJSON is ScenarioVersionRecord.BodyJSON — the canonical bytes
	// Digest was computed from; internal/content/http serves this
	// verbatim as the wire "body" instead of re-marshaling Body.
	BodyJSON []byte
	Digest   [32]byte
}

// VersionSummary is one row of GET /scenarios/{id}/versions —
// version metadata without the (potentially large) body.
type VersionSummary struct {
	ID         uuid.UUID
	Version    int
	Status     string
	Digest     [32]byte
	Difficulty int
	CreatedBy  uuid.UUID
	CreatedAt  time.Time
	ApprovedBy *uuid.UUID
	ApprovedAt *time.Time
}

// ScenarioFilter is GET /scenarios' query (openapi.yaml): every field is
// optional — a zero value (empty string, zero int) means "no filter on
// this dimension" except Page/PageSize, which the HTTP layer always
// resolves to a positive default before calling Service.
type ScenarioFilter struct {
	TargetService string
	ExerciseType  ExerciseType
	Status        string
	DifficultyMin int
	DifficultyMax int
	Page          int
	PageSize      int
}

// Store is the narrow persistence port Service needs (CLAUDE.md: "declare
// [interfaces] near the consuming application service"), satisfied by
// internal/content/postgres.Store and, in tests, by a fake. Every method
// but WithTx takes an explicit pgx.Tx — the same convention
// internal/auth's Store uses and for the same reason (service.go there).
type Store interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error

	// These reference lookups are also how storeCatalog (import.go)
	// implements Catalog during ImportScenarios.
	ServiceByCode(ctx context.Context, tx pgx.Tx, code string) (ServiceRecord, error)
	ListServices(ctx context.Context, tx pgx.Tx) ([]ServiceRecord, error)
	InsertService(ctx context.Context, tx pgx.Tx, s ServiceRecord) error
	LatestIntakeCatalog(ctx context.Context, tx pgx.Tx) (IntakeCatalog, error)
	IntakeCatalogByVersion(ctx context.Context, tx pgx.Tx, version int) (IntakeCatalog, error)
	InsertIntakeCatalog(ctx context.Context, tx pgx.Tx, catalog IntakeCatalog) error

	ClassifierTypeByCode(ctx context.Context, tx pgx.Tx, code string) (ClassifierType, error)
	InsertClassifierType(ctx context.Context, tx pgx.Tx, c ClassifierType) error

	// ScenarioByKey locks the row FOR UPDATE (a concurrent import of the
	// same key must serialize, not race two "new scenario" inserts) —
	// ErrNotFound when no scenario has this source_key yet.
	ScenarioByKey(ctx context.Context, tx pgx.Tx, key string) (ScenarioRecord, error)
	ScenarioByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (ScenarioRecord, error)
	InsertScenario(ctx context.Context, tx pgx.Tx, s ScenarioRecord) error
	UpdateScenarioDifficulty(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID, difficulty int) error
	ListScenarios(ctx context.Context, tx pgx.Tx, filter ScenarioFilter) ([]ScenarioSummary, int, error)

	// MaxVersion returns 0 (not ErrNotFound) when the scenario has no
	// version yet — a scenario row and its first version are always
	// inserted in the same call sequence, but the type stays honest about
	// "zero versions" being a real, checkable state during import.
	MaxVersion(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID) (int, error)
	VersionReferenceByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (ScenarioVersionReference, error)
	VersionReferenceBySourceKeyVersion(ctx context.Context, tx pgx.Tx, key string, version int) (ScenarioVersionReference, error)
	// VersionByID is the full record a consumer outside content needs
	// when it only has a version id to start from (training's
	// assignment/start, slice 3's C4) — VersionReferenceByID's narrower
	// projection is not enough once the caller must also read Body.Card/
	// Events/Reference, not just check compatibility.
	VersionByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (ScenarioVersionRecord, error)
	VersionByNumber(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID, version int) (ScenarioVersionRecord, error)
	ApprovedVersion(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID) (ScenarioVersionRecord, error)
	ListVersions(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID) ([]VersionSummary, error)
	InsertScenarioVersion(ctx context.Context, tx pgx.Tx, v ScenarioVersionRecord) (ScenarioVersionRecord, error)
	// SupersedeApprovedVersion moves the scenario's current approved
	// version (if any) to superseded, preserving its approved_by/
	// approved_at (scenario_versions_approval_shape, migrations/00004).
	// It is a no-op (found=false) for a scenario's first version.
	SupersedeApprovedVersion(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID) (found bool, err error)

	AuditRecord(ctx context.Context, tx pgx.Tx, entry audit.Entry) error
}
