package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"

	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ImportCount is ImportServices/ImportClassifierTypes' result — how many
// rows a seed file added versus how many already matched what was
// stored (slice-2-plan.md's "повторная загрузка не создаёт дубликаты").
type ImportCount struct {
	Created   int
	Unchanged int
}

type serviceFileEntry struct {
	Code     string   `json:"code"`
	Name     string   `json:"name"`
	Workflow Workflow `json:"workflow"`
	Active   *bool    `json:"active,omitempty"`
}

// DecodeServiceDefs parses a services.json seed file (an array of
// {code, name, workflow, active?} — active defaults to true).
func DecodeServiceDefs(r io.Reader) ([]ServiceRecord, error) {
	var entries []serviceFileEntry
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode services file: %w", err)
	}
	records := make([]ServiceRecord, len(entries))
	for i, e := range entries {
		if e.Code == "" {
			return nil, invalid(fmt.Sprintf("[%d].code", i), "required")
		}
		if e.Name == "" {
			return nil, invalid(fmt.Sprintf("[%d].name", i), "required")
		}
		active := true
		if e.Active != nil {
			active = *e.Active
		}
		records[i] = ServiceRecord{Code: e.Code, Name: e.Name, Workflow: e.Workflow, Active: active}
	}
	return records, nil
}

type classifierFileEntry struct {
	Code      string         `json:"code"`
	Name      string         `json:"name"`
	Features  map[string]any `json:"features"`
	Notify    []string       `json:"notify"`
	SourceRow *int           `json:"source_row,omitempty"`
}

// DecodeClassifierDefs parses a classifier.json seed file (an array of
// {code, name, features, notify, source_row?}).
func DecodeClassifierDefs(r io.Reader) ([]ClassifierType, error) {
	var entries []classifierFileEntry
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode classifier file: %w", err)
	}
	types := make([]ClassifierType, len(entries))
	for i, e := range entries {
		if e.Code == "" {
			return nil, invalid(fmt.Sprintf("[%d].code", i), "required")
		}
		if e.Name == "" {
			return nil, invalid(fmt.Sprintf("[%d].name", i), "required")
		}
		types[i] = ClassifierType{
			ID: uuid.New(), Code: e.Code, Name: e.Name,
			Features: e.Features, Notify: e.Notify, SourceRow: e.SourceRow,
		}
	}
	return types, nil
}

// ImportServices upserts services: a code the store does not have yet is
// inserted; a code it already has is a no-op if the stored definition
// matches, or ErrConflict if it does not (slice-2-plan.md's C3: "вставка
// отсутствующих, идентичные — no-op, отличающиеся — ошибка"). The whole
// file commits atomically with one audit row.
func (s *Service) ImportServices(ctx context.Context, r io.Reader, actorID uuid.UUID, actorRole, requestID string) (ImportCount, error) {
	defs, err := DecodeServiceDefs(r)
	if err != nil {
		return ImportCount{}, err
	}
	seen := make(map[string]bool, len(defs))
	for _, d := range defs {
		if seen[d.Code] {
			return ImportCount{}, invalid("code", fmt.Sprintf("duplicate_in_file:%s", d.Code))
		}
		seen[d.Code] = true
	}

	var result ImportCount
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		existing, err := s.store.ListServices(ctx, tx)
		if err != nil {
			return err
		}
		byCode := make(map[string]ServiceRecord, len(existing))
		for _, e := range existing {
			byCode[e.Code] = e
		}

		for _, d := range defs {
			if cur, ok := byCode[d.Code]; ok {
				if serviceRecordsEqual(cur, d) {
					result.Unchanged++
					continue
				}
				return conflict("service:"+d.Code, "definition_changed")
			}
			if err := s.store.InsertService(ctx, tx, d); err != nil {
				return err
			}
			result.Created++
		}
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actorID, ActorRole: actorRole, Action: "content.import.services",
			ResourceType: "service", Outcome: audit.OutcomeOK, RequestID: requestID,
			Details: map[string]any{"created": result.Created, "unchanged": result.Unchanged},
		})
	})
	if err != nil {
		return ImportCount{}, err
	}
	return result, nil
}

func serviceRecordsEqual(a, b ServiceRecord) bool {
	return a.Name == b.Name && a.Active == b.Active && reflect.DeepEqual(a.Workflow, b.Workflow)
}

// ImportClassifierTypes upserts classifier types with the same
// insert-or-no-op-or-conflict semantics as ImportServices.
func (s *Service) ImportClassifierTypes(ctx context.Context, r io.Reader, actorID uuid.UUID, actorRole, requestID string) (ImportCount, error) {
	defs, err := DecodeClassifierDefs(r)
	if err != nil {
		return ImportCount{}, err
	}
	seen := make(map[string]bool, len(defs))
	for _, d := range defs {
		if seen[d.Code] {
			return ImportCount{}, invalid("code", fmt.Sprintf("duplicate_in_file:%s", d.Code))
		}
		seen[d.Code] = true
	}

	var result ImportCount
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		for _, d := range defs {
			cur, lookupErr := s.store.ClassifierTypeByCode(ctx, tx, d.Code)
			if lookupErr == nil {
				if classifierTypesEqual(cur, d) {
					result.Unchanged++
					continue
				}
				return conflict("classifier_type:"+d.Code, "definition_changed")
			}
			if !errors.Is(lookupErr, ErrNotFound) {
				return lookupErr
			}
			if err := s.store.InsertClassifierType(ctx, tx, d); err != nil {
				return err
			}
			result.Created++
		}
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actorID, ActorRole: actorRole, Action: "content.import.classifier",
			ResourceType: "classifier_type", Outcome: audit.OutcomeOK, RequestID: requestID,
			Details: map[string]any{"created": result.Created, "unchanged": result.Unchanged},
		})
	})
	if err != nil {
		return ImportCount{}, err
	}
	return result, nil
}

func classifierTypesEqual(a, b ClassifierType) bool {
	return a.Name == b.Name && reflect.DeepEqual(a.Features, b.Features) && reflect.DeepEqual(a.Notify, b.Notify)
}

// ScenarioImportCount is ImportScenarios' result.
type ScenarioImportCount struct {
	NewScenarios int
	NewVersions  int // includes NewScenarios' first version
	Unchanged    int
}

// ImportScenarios validates and imports a batch of scenario files
// (seed/scenarios/*.json) in one transaction — an invalid file, a
// (key, version) collision within the batch, or an ErrConflict against
// already-stored content rolls the whole batch back, so the catalogue
// never ends up partially updated (slice-2-plan.md's C3). files is keyed
// by filename only for error messages; ordering (sorted by filename) only
// affects which duplicate is reported first, not the outcome.
func (s *Service) ImportScenarios(ctx context.Context, files map[string]io.Reader, actorID uuid.UUID, actorRole, requestID string) (ScenarioImportCount, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	type decodedFile struct {
		name   string
		file   File
		digest [32]byte
	}
	items := make([]decodedFile, 0, len(names))
	seenKeyVersion := make(map[string]string, len(names))
	for _, name := range names {
		raw, file, err := DecodeFile(files[name])
		if err != nil {
			return ScenarioImportCount{}, fmt.Errorf("%s: %w", name, err)
		}
		if err := s.schemaValidator.ValidateFile(raw); err != nil {
			return ScenarioImportCount{}, fmt.Errorf("%s: %w: %v", name, ErrSchemaInvalid, err)
		}
		digest, err := BodyDigest(raw)
		if err != nil {
			return ScenarioImportCount{}, fmt.Errorf("%s: %w", name, err)
		}
		key := fmt.Sprintf("%s@%d", file.Key, file.Version)
		if other, dup := seenKeyVersion[key]; dup {
			return ScenarioImportCount{}, fmt.Errorf("%s: %s and %s both claim (key=%s, version=%d)", name, other, name, file.Key, file.Version)
		}
		seenKeyVersion[key] = name
		items = append(items, decodedFile{name: name, file: file, digest: digest})
	}

	var result ScenarioImportCount
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		catalog := storeCatalog{ctx: ctx, tx: tx, store: s.store}
		for _, it := range items {
			if err := Validate(it.file.Body, catalog); err != nil {
				return fmt.Errorf("%s: %w", it.name, err)
			}
			change, err := s.importOneScenarioVersion(ctx, tx, it.file, it.digest, actorID)
			if err != nil {
				return fmt.Errorf("%s: %w", it.name, err)
			}
			switch change {
			case scenarioChangeNew:
				result.NewScenarios++
				result.NewVersions++
			case scenarioChangeNewVersion:
				result.NewVersions++
			case scenarioChangeNone:
				result.Unchanged++
			}
		}
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actorID, ActorRole: actorRole, Action: "content.import.scenarios",
			ResourceType: "scenario", Outcome: audit.OutcomeOK, RequestID: requestID,
			Details: map[string]any{
				"new_scenarios": result.NewScenarios, "new_versions": result.NewVersions, "unchanged": result.Unchanged,
			},
		})
	})
	if err != nil {
		return ScenarioImportCount{}, err
	}
	return result, nil
}

type scenarioChange int

const (
	scenarioChangeNone scenarioChange = iota
	scenarioChangeNewVersion
	scenarioChangeNew
)

// importOneScenarioVersion applies slice-2-plan.md's C3 version rules for
// one already schema- and semantically-validated file:
//   - unknown key: version must be 1; creates the scenario and its first
//     (approved) version.
//   - known key: title/target_service/origin are fixed by the key — any
//     change is ErrConflict, even if the body would otherwise be
//     identical; version == current max: no-op if the digest matches,
//     else ErrConflict (someone tried to redefine an existing version);
//     version < max: ErrConflict (an old version can't be "returned to");
//     version == max+1: creates the new approved version and supersedes
//     the previous one; version > max+1: ErrConflict (no gaps).
func (s *Service) importOneScenarioVersion(ctx context.Context, tx pgx.Tx, file File, digest [32]byte, actorID uuid.UUID) (scenarioChange, error) {
	existing, err := s.store.ScenarioByKey(ctx, tx, file.Key)
	if errors.Is(err, ErrNotFound) {
		if file.Version != 1 {
			return 0, conflict(fmt.Sprintf("scenario:%s", file.Key), "version_gap")
		}
		scenarioID := uuid.New()
		sourceKey := file.Key
		if err := s.store.InsertScenario(ctx, tx, ScenarioRecord{
			ID: scenarioID, Title: file.Title, TargetService: file.Body.TargetService,
			Difficulty: file.Body.Difficulty, Origin: file.Origin, Status: "approved",
			SourceKey: &sourceKey, CreatedBy: actorID,
		}); err != nil {
			return 0, err
		}
		if _, err := s.store.InsertScenarioVersion(ctx, tx, ScenarioVersionRecord{
			ID: uuid.New(), ScenarioID: scenarioID, Version: 1, Status: "approved",
			Body: file.Body, Digest: digest, Difficulty: file.Body.Difficulty, CreatedBy: actorID,
		}); err != nil {
			return 0, err
		}
		return scenarioChangeNew, nil
	}
	if err != nil {
		return 0, err
	}

	if existing.Title != file.Title {
		return 0, conflict(fmt.Sprintf("scenario:%s", file.Key), "title_changed")
	}
	if existing.TargetService != file.Body.TargetService {
		return 0, conflict(fmt.Sprintf("scenario:%s", file.Key), "target_service_changed")
	}
	if existing.Origin != file.Origin {
		return 0, conflict(fmt.Sprintf("scenario:%s", file.Key), "origin_changed")
	}

	maxVersion, err := s.store.MaxVersion(ctx, tx, existing.ID)
	if err != nil {
		return 0, err
	}

	switch {
	case file.Version == maxVersion:
		current, err := s.store.VersionByNumber(ctx, tx, existing.ID, file.Version)
		if err != nil {
			return 0, err
		}
		if current.Digest != digest {
			return 0, conflict(fmt.Sprintf("scenario:%s@%d", file.Key, file.Version), "content_changed")
		}
		return scenarioChangeNone, nil
	case file.Version < maxVersion:
		return 0, conflict(fmt.Sprintf("scenario:%s@%d", file.Key, file.Version), "version_regression")
	case file.Version > maxVersion+1:
		return 0, conflict(fmt.Sprintf("scenario:%s@%d", file.Key, file.Version), "version_gap")
	}

	if _, err := s.store.SupersedeApprovedVersion(ctx, tx, existing.ID); err != nil {
		return 0, err
	}
	if _, err := s.store.InsertScenarioVersion(ctx, tx, ScenarioVersionRecord{
		ID: uuid.New(), ScenarioID: existing.ID, Version: file.Version, Status: "approved",
		Body: file.Body, Digest: digest, Difficulty: file.Body.Difficulty, CreatedBy: actorID,
	}); err != nil {
		return 0, err
	}
	if err := s.store.UpdateScenarioDifficulty(ctx, tx, existing.ID, file.Body.Difficulty); err != nil {
		return 0, err
	}
	return scenarioChangeNewVersion, nil
}

// storeCatalog adapts a live Store+tx into the pure-value Catalog
// Validate needs (validate.go's doc comment): each lookup is one query
// against the transaction's own view, so a scenario file can reference a
// service/classifier type imported earlier in the same "import seed" run
// even though it was not yet committed when this process started.
type storeCatalog struct {
	ctx   context.Context
	tx    pgx.Tx
	store Store
}

var _ Catalog = storeCatalog{}

func (c storeCatalog) Service(code string) (ServiceRecord, bool) {
	r, err := c.store.ServiceByCode(c.ctx, c.tx, code)
	if err != nil {
		return ServiceRecord{}, false
	}
	return r, true
}

func (c storeCatalog) ClassifierType(code string) (string, bool) {
	t, err := c.store.ClassifierTypeByCode(c.ctx, c.tx, code)
	if err != nil {
		return "", false
	}
	return t.Name, true
}
