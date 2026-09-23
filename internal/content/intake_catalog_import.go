package content

import (
	"context"
	"errors"
	"io"

	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ImportIntakeCatalog inserts a complete immutable catalog revision. Replaying
// identical bytes is idempotent; changing a published revision is a conflict.
func (s *Service) ImportIntakeCatalog(ctx context.Context, r io.Reader, actorID uuid.UUID, actorRole, requestID string) (ImportCount, error) {
	c, err := DecodeIntakeCatalog(r)
	if err != nil {
		return ImportCount{}, err
	}
	var result ImportCount
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		// Serialize imports of different versions as well as the same version.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(1123)`); err != nil {
			return ErrStorage
		}
		latest, err := s.store.LatestIntakeCatalog(ctx, tx)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil && c.Version <= latest.Version {
			existing, err := s.store.IntakeCatalogByVersion(ctx, tx, c.Version)
			if err != nil {
				return conflict("intake_catalog", "version_regression")
			}
			if !EqualIntakeCatalog(existing, c) {
				return conflict("intake_catalog", "content_changed")
			}
			result.Unchanged = 1
		} else {
			if err == nil && c.Version != latest.Version+1 {
				return conflict("intake_catalog", "version_gap")
			}
			if err != nil && c.Version != 1 {
				return conflict("intake_catalog", "version_gap")
			}
			for _, rule := range c.ServiceRules {
				service, err := s.store.ServiceByCode(ctx, tx, rule.ServiceCode)
				if err != nil || !service.Active {
					return invalid("service_rules", "unknown_or_inactive_service:"+rule.ServiceCode)
				}
			}
			if err := s.store.InsertIntakeCatalog(ctx, tx, c); err != nil {
				return err
			}
			result.Created = 1
		}
		return s.store.AuditRecord(ctx, tx, audit.Entry{ActorID: &actorID, ActorRole: actorRole,
			Action: "content.import.intake_catalog", ResourceType: "intake_catalog",
			Outcome: audit.OutcomeOK, RequestID: requestID,
			Details: map[string]any{"version": c.Version, "created": result.Created}})
	})
	if err != nil {
		return ImportCount{}, err
	}
	return result, nil
}
