package audit

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer is the part of a pool or transaction Prune needs.
type Execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// Prune deletes audit_log rows older than retentionDays by PostgreSQL's
// own clock (RFC-001 §9 keeps audit 6 months; ADR-033's audit.prune), in
// batches of at most batch rows, each its own statement — one huge
// DELETE would hold locks and bloat WAL for the whole table at once. It
// returns how many rows it removed. Safe to repeat: a retry after a
// failure only finds what is left.
func Prune(ctx context.Context, db Execer, retentionDays, batch int) (int64, error) {
	if db == nil || retentionDays < 0 || batch < 1 {
		return 0, ErrInvalidEntry
	}
	var total int64
	for {
		tag, err := db.Exec(ctx, `
			DELETE FROM audit_log
			WHERE id IN (
				SELECT id FROM audit_log
				WHERE at < now() - make_interval(days => $1)
				ORDER BY id
				LIMIT $2
			)`, retentionDays, batch)
		if err != nil {
			return total, ErrStorage
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < int64(batch) {
			return total, nil
		}
	}
}
