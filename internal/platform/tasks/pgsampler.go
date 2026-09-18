// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/postgres/observability.go; adapted: only tasks — no runs table
// was ported (docs/technical-discovery.md §3.5).
package tasks

import (
	"context"

	"emsim/internal/platform/observability"

	"github.com/jackc/pgx/v5/pgxpool"
)

type SamplerStore struct{ pool *pgxpool.Pool }

func NewSamplerStore(pool *pgxpool.Pool) *SamplerStore { return &SamplerStore{pool: pool} }

func (s *SamplerStore) Sample(ctx context.Context) (observability.Snapshot, error) {
	if s == nil || s.pool == nil {
		return observability.Snapshot{}, ErrStorage
	}
	rows, err := s.pool.Query(ctx, `SELECT kind, status, count(*) FROM tasks GROUP BY kind, status`)
	if err != nil {
		return observability.Snapshot{}, ErrStorage
	}
	defer rows.Close()
	snapshot := observability.Snapshot{}
	for rows.Next() {
		var count observability.TaskCount
		if err := rows.Scan(&count.Kind, &count.Status, &count.Count); err != nil {
			return observability.Snapshot{}, ErrStorage
		}
		snapshot.Tasks = append(snapshot.Tasks, count)
	}
	if err := rows.Err(); err != nil {
		return observability.Snapshot{}, ErrStorage
	}
	return snapshot, nil
}
