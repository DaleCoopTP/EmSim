package content

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ListServices returns every service (active or not) — GET /services'
// admin+instructor route (authz.GroupServices).
func (s *Service) ListServices(ctx context.Context) ([]ServiceRecord, error) {
	var records []ServiceRecord
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		r, err := s.store.ListServices(ctx, tx)
		records = r
		return err
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

// ListScenarios is GET /scenarios (instructor, authz.GroupContent).
func (s *Service) ListScenarios(ctx context.Context, filter ScenarioFilter) ([]ScenarioSummary, int, error) {
	var items []ScenarioSummary
	var total int
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		i, t, err := s.store.ListScenarios(ctx, tx, filter)
		items, total = i, t
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ScenarioDetail is GET /scenarios/{id} — the scenario plus its current
// approved version's full content (ErrNotFound if either the scenario or
// an approved version does not exist).
func (s *Service) ScenarioDetail(ctx context.Context, id uuid.UUID) (ScenarioDetail, error) {
	var detail ScenarioDetail
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		sc, err := s.store.ScenarioByID(ctx, tx, id)
		if err != nil {
			return err
		}
		v, err := s.store.ApprovedVersion(ctx, tx, id)
		if err != nil {
			return err
		}
		detail = ScenarioDetail{
			ScenarioSummary: ScenarioSummary{ScenarioRecord: sc, Version: v.Version, HasEvents: len(v.Body.Events) > 0},
			VersionID:       v.ID,
			Body:            v.Body,
			Digest:          v.Digest,
		}
		return nil
	})
	if err != nil {
		return ScenarioDetail{}, err
	}
	return detail, nil
}

// ScenarioVersions is GET /scenarios/{id}/versions.
func (s *Service) ScenarioVersions(ctx context.Context, id uuid.UUID) ([]VersionSummary, error) {
	var versions []VersionSummary
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.store.ScenarioByID(ctx, tx, id); err != nil {
			return err
		}
		v, err := s.store.ListVersions(ctx, tx, id)
		versions = v
		return err
	})
	if err != nil {
		return nil, err
	}
	return versions, nil
}

// ScenarioPreview is GET /scenarios/{id}/preview — openapi.yaml's
// {card: CardPreview, reference: ScenarioReference}: Card goes through
// the same allowlist projection a trainee will see (slice 3); Reference
// is the full эталон, unmodified, since this route is instructor-only
// (authz.GroupContent).
type ScenarioPreview struct {
	Card      CardPreview
	Contacts  []ContactPreview
	Reference Reference
}

func (s *Service) ScenarioPreview(ctx context.Context, id uuid.UUID) (ScenarioPreview, error) {
	var preview ScenarioPreview
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		v, err := s.store.ApprovedVersion(ctx, tx, id)
		if err != nil {
			return err
		}
		preview = ScenarioPreview{
			Card:      ProjectCard(v.Body.Card),
			Contacts:  ProjectContacts(v.Body.Contacts),
			Reference: v.Body.Reference,
		}
		return nil
	})
	if err != nil {
		return ScenarioPreview{}, err
	}
	return preview, nil
}
