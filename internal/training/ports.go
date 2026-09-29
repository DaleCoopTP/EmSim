package training

import (
	"context"

	"emsim/internal/auth"
	"emsim/internal/content"
	"emsim/internal/platform/tasks"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type UserDirectory interface {
	UserByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (auth.User, error)
	ListUsers(ctx context.Context, tx pgx.Tx, page, pageSize int) ([]auth.User, int, error)
}

type WorkstationDirectory interface {
	WorkstationByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (auth.Workstation, error)
	WorkstationByNumber(ctx context.Context, tx pgx.Tx, number int) (auth.Workstation, error)
	ListWorkstations(ctx context.Context, tx pgx.Tx) ([]auth.Workstation, error)
}

// ScenarioReader is training's read of content's approved scenario
// versions — needed at assignment time (compatibility checks) and at
// start (the card/workflow/reference an item is created from). The
// method name matches content.Store.VersionByID exactly, not a
// training-local rename, so *contentpg.Store satisfies this
// structurally.
type ScenarioReader interface {
	VersionByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (content.ScenarioVersionRecord, error)
	ScenarioByKey(ctx context.Context, tx pgx.Tx, key string) (content.ScenarioRecord, error)
	// ScenarioByID is 112-7/ADR-027's own addition — StartPreview needs
	// created_by to check that the actor starting a preview run on a
	// draft version actually owns that scenario.
	ScenarioByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (content.ScenarioRecord, error)
	VersionByNumber(ctx context.Context, tx pgx.Tx, scenarioID uuid.UUID, version int) (content.ScenarioVersionRecord, error)
	LatestIntakeCatalog(ctx context.Context, tx pgx.Tx) (content.IntakeCatalog, error)
	IntakeCatalogByVersion(ctx context.Context, tx pgx.Tx, version int) (content.IntakeCatalog, error)
	// ListApprovedDDSVersions is ДДС-6/ADR-035's random queue fill source.
	ListApprovedDDSVersions(ctx context.Context, tx pgx.Tx, targetService string) ([]content.ScenarioVersionRecord, error)
}

// ServiceReader is training's read of content's service workflow
// snapshots — an item's own Workflow field is copied from this at offer
// time (ADR-017/RFC-001 §6's immutability principle extended to a
// service's workflow, not just a scenario version's body).
type ServiceReader interface {
	ServiceByCode(ctx context.Context, tx pgx.Tx, code string) (content.ServiceRecord, error)
	ListServices(ctx context.Context, tx pgx.Tx) ([]content.ServiceRecord, error)
}

type TaskEnqueuer interface {
	EnqueueTx(ctx context.Context, tx pgx.Tx, request tasks.EnqueueRequest) (id uuid.UUID, created bool, err error)
	EnqueueWaitingTx(ctx context.Context, tx pgx.Tx, request tasks.EnqueueWaitingRequest) (id uuid.UUID, created bool, err error)
}
