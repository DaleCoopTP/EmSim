package content

import (
	"context"
	"errors"

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

// ServiceExists reports whether code names a known service — content's
// side of internal/auth's ServiceCatalog port (slice-2-plan.md's C5).
// Service satisfies auth.ServiceCatalog structurally by having this
// method; content never imports auth (CLAUDE.md: consumer-owned ports).
// An active-or-not service both counts: a service being deactivated does
// not retroactively make an already-assigned trainee's service_code
// invalid, and CreateUser/UpdateUser's own role rules are what actually
// restrict which role may carry one.
func (s *Service) ServiceExists(ctx context.Context, code string) (bool, error) {
	var exists bool
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := s.store.ServiceByCode(ctx, tx, code)
		if errors.Is(err, ErrNotFound) {
			exists = false
			return nil
		}
		if err != nil {
			return err
		}
		exists = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return exists, nil
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
