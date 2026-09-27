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
			ScenarioSummary: ScenarioSummary{ScenarioRecord: sc, ExerciseType: v.Body.ExerciseType, Version: v.Version, HasEvents: len(v.Body.Events) > 0},
			VersionID:       v.ID,
			Body:            v.Body,
			BodyJSON:        v.BodyJSON,
			Digest:          v.Digest,
		}
		return nil
	})
	if err != nil {
		return ScenarioDetail{}, err
	}
	return detail, nil
}

// ScenarioVersions is GET /scenarios/{id}/versions. The scenario's own
// author sees every version; any other instructor sees only published
// ones (approved_at set — the current approved and any earlier approved,
// now superseded, version), never a draft or a superseded draft, and
// ErrNotFound for a scenario that has no published version at all
// (ADR-027: someone else's draft is invisible, not merely off-limits).
func (s *Service) ScenarioVersions(ctx context.Context, actorID, id uuid.UUID) ([]VersionSummary, error) {
	var versions []VersionSummary
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		sc, err := s.store.ScenarioByID(ctx, tx, id)
		if err != nil {
			return err
		}
		all, err := s.store.ListVersions(ctx, tx, id)
		if err != nil {
			return err
		}
		if sc.CreatedBy == actorID {
			versions = all
			return nil
		}
		for _, v := range all {
			if v.ApprovedAt != nil {
				versions = append(versions, v)
			}
		}
		if len(versions) == 0 {
			return ErrNotFound
		}
		return nil
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
	ExerciseType ExerciseType
	Intake112    *Intake112
	Card         CardPreview
	Contacts     []ContactPreview
	Reference    Reference
}

func (s *Service) ScenarioPreview(ctx context.Context, id uuid.UUID) (ScenarioPreview, error) {
	var preview ScenarioPreview
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		v, err := s.store.ApprovedVersion(ctx, tx, id)
		if err != nil {
			return err
		}
		preview.ExerciseType = v.Body.ExerciseType
		if v.Body.ExerciseType == ExerciseTypeOperator112Intake {
			preview.Intake112 = v.Body.Intake112
		} else {
			preview.Card = ProjectCard(v.Body.Card)
			preview.Contacts = ProjectContacts(v.Body.Contacts)
			preview.Reference = v.Body.Reference
		}
		return nil
	})
	if err != nil {
		return ScenarioPreview{}, err
	}
	return preview, nil
}
