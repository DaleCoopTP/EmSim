package training

import (
	"context"

	"emsim/internal/auth"
	"emsim/internal/content"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UserDirectory/WorkstationDirectory/ScenarioReader/ServiceReader are
// training's consumer-owned ports (CLAUDE.md: "небольшие интерфейсы-
// порты объявляются со стороны потребителя") onto auth's and content's
// own tables — training never writes them, only reads, so no module
// boundary is crossed by declaring these here. Each method signature is
// copied verbatim from an existing method on *authpg.Store/*contentpg.
// Store, so those concrete types already satisfy these interfaces
// structurally: cmd/emsim's composition (slice 3's C5) and this
// package's own integration tests construct a Service by passing an
// *authpg.Store/*contentpg.Store directly, with no adapter type needed.
// Every method takes the caller's own pgx.Tx (not *auth.Service/
// *content.Service, which open their own transaction internally) so a
// training transaction can read auth/content data without a second
// PostgreSQL connection — see LOG.MD's 2026-09-20 entry on the
// pool_max_conns=1 deadlock that pattern caused elsewhere.
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
}

// ServiceReader is training's read of content's service workflow
// snapshots — an item's own Workflow field is copied from this at offer
// time (ADR-017/RFC-001 §6's immutability principle extended to a
// service's workflow, not just a scenario version's body).
type ServiceReader interface {
	ServiceByCode(ctx context.Context, tx pgx.Tx, code string) (content.ServiceRecord, error)
	ListServices(ctx context.Context, tx pgx.Tx) ([]content.ServiceRecord, error)
}
