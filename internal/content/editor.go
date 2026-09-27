package content

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// This file is the operator-112 scenario editor's own service layer
// (112-7/ADR-027): create/copy, save (always a new version — ADR-027's
// "Владение и жизненный цикл версий"), validate-without-saving, the
// phrase tester, and approve. It only ever touches
// exercise_type=operator112_intake, mode=full_case, caller_mode=
// free_text scenarios — the editor's own scope (slice-112-7-plan.md §2
// decision 4); every entry point below rejects anything else with
// ErrUnsupportedForEditor before touching the store. DDS's own future
// editor (slice-planning.md's srez 11) is a separate implementation.

var (
	// ErrStaleDraft is PUT/approve's own optimistic-concurrency
	// rejection (409 stale_draft) — base_digest did not match the
	// version's current digest.
	ErrStaleDraft = errors.New("stale draft")
	// ErrUnsupportedForEditor is 422 unsupported_for_editor — the
	// scenario/version this call targets is not full_case+free_text, or
	// (for create) exercise_type is not operator112_intake.
	ErrUnsupportedForEditor = errors.New("scenario not supported by the operator-112 editor")
)

// BlockingIssuesError is preview/approve's own 422 has_blocking_issues —
// at least one Severity=error ValidationIssue remains.
type BlockingIssuesError struct{ Issues []ValidationIssue }

func (e *BlockingIssuesError) Error() string { return "scenario has blocking validation issues" }

func hasBlockingIssue(issues []ValidationIssue) bool {
	for _, issue := range issues {
		if issue.Severity == SeverityError {
			return true
		}
	}
	return false
}

// EditorScenario is the owner's own view of a scenario for the 112-7
// editor: the scenario record plus its latest version regardless of
// status (draft, approved, or — immediately after a fresh approve within
// the same request — the version just approved). Unlike ScenarioDetail/
// ScenarioPreview (read.go), which only ever resolve the current
// *approved* version and 404 otherwise, this always resolves whatever
// version is highest-numbered, which is the only one an owner can still
// be editing.
type EditorScenario struct {
	ScenarioRecord
	VersionID uuid.UUID
	Version   int
	Status    string
	Body      Body
	BodyJSON  []byte
	Digest    [32]byte
	Issues    []ValidationIssue
}

// ScenarioCreateInput is POST /scenarios' own request (openapi.yaml's
// ScenarioCreate) — exactly one of Body/CopyFromVersionID is set; the
// HTTP layer enforces that before calling in.
type ScenarioCreateInput struct {
	Title             string
	Difficulty        int
	Body              *Body
	CopyFromVersionID *uuid.UUID
}

func operator112EditorEligible(body Body) bool {
	return body.ExerciseType == ExerciseTypeOperator112Intake && body.Intake112 != nil &&
		body.Intake112.Mode == "full_case" && body.Intake112.CallerMode == CallerModeFreeText
}

// CreateOperator112Scenario is POST /scenarios: either copies an
// existing full_case+free_text version (own, another instructor's
// approved one, or a file-imported seed — never someone else's
// unapproved draft, see readVersionForCopy) into a brand-new scenario under the
// caller's own authorship, or starts one from a caller-supplied body. A
// structurally invalid body (e.g. still-empty dialogue.facts on a fresh
// scenario) is not rejected here — Issues carries whatever
// ValidateDetailed finds, the same "save with errors allowed" rule PUT
// follows (ADR-027): a brand-new scenario is definitionally unfinished.
func (s *Service) CreateOperator112Scenario(ctx context.Context, actorID uuid.UUID, in ScenarioCreateInput) (EditorScenario, error) {
	var body Body
	switch {
	case in.CopyFromVersionID != nil:
		src, err := s.readVersionForCopy(ctx, actorID, *in.CopyFromVersionID)
		if err != nil {
			return EditorScenario{}, err
		}
		body = src
	case in.Body != nil:
		body = *in.Body
	default:
		return EditorScenario{}, invalid("body", "required")
	}
	if !operator112EditorEligible(body) {
		return EditorScenario{}, ErrUnsupportedForEditor
	}
	body.Difficulty = in.Difficulty

	_, canonicalJSON, digest, err := s.canonicalizeOperator112Body(body)
	if err != nil {
		return EditorScenario{}, err
	}

	var result EditorScenario
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		scenarioID := uuid.New()
		if err := s.store.InsertScenario(ctx, tx, ScenarioRecord{
			ID: scenarioID, Title: in.Title, Difficulty: in.Difficulty, Origin: "manual",
			Status: "draft", CreatedBy: actorID,
		}); err != nil {
			return err
		}
		v, err := s.store.InsertScenarioVersion(ctx, tx, ScenarioVersionRecord{
			ID: uuid.New(), ScenarioID: scenarioID, Version: 1, Status: "draft",
			Body: body, BodyJSON: canonicalJSON, Digest: digest, Difficulty: in.Difficulty, CreatedBy: actorID,
		})
		if err != nil {
			return err
		}
		sc, err := s.store.ScenarioByID(ctx, tx, scenarioID)
		if err != nil {
			return err
		}
		issues, err := ValidateDetailed(body, storeCatalog{ctx: ctx, tx: tx, store: s.store})
		if err != nil {
			return err
		}
		if err := s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actorID, ActorRole: "instructor", Action: "scenario.create",
			ResourceType: "scenario", ResourceID: &scenarioID, Outcome: audit.OutcomeOK,
		}); err != nil {
			return err
		}
		result = EditorScenario{
			ScenarioRecord: sc, VersionID: v.ID, Version: v.Version, Status: v.Status,
			Body: v.Body, BodyJSON: v.BodyJSON, Digest: v.Digest, Issues: issues,
		}
		return nil
	})
	if err != nil {
		return EditorScenario{}, err
	}
	return result, nil
}

// readVersionForCopy loads a source version by id for POST /scenarios'
// copy_from_version_id. The source may be the caller's own version (any
// status) or any published one — another instructor's approved scenario
// or a file-imported seed; the copy then becomes an independent new
// scenario under the caller's own authorship (ADR-027's "Копирование").
// Someone else's never-approved draft is ErrNotFound, exactly like a
// direct read of it: copying must not become a way around draft privacy.
func (s *Service) readVersionForCopy(ctx context.Context, actorID, versionID uuid.UUID) (Body, error) {
	var body Body
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		v, err := s.store.VersionByID(ctx, tx, versionID)
		if err != nil {
			return err
		}
		if v.ApprovedAt == nil {
			sc, err := s.store.ScenarioByID(ctx, tx, v.ScenarioID)
			if err != nil {
				return err
			}
			if sc.CreatedBy != actorID {
				return ErrNotFound
			}
		}
		body = v.Body
		return nil
	})
	return body, err
}

// ownedLatestVersion loads scenarioID's own scenario row and its
// highest-numbered version (draft or approved — whichever is latest),
// rejecting with ErrNotFound (never ErrForbidden — ADR-027: a chужой
// draft is invisible, not merely off-limits) unless actorID is its
// created_by. forUpdate locks the scenario row first, so concurrent
// saves/approves of one scenario run one after another: the second then
// sees the first's new latest version and gets ErrStaleDraft rather
// than a unique-violation on the next version number.
func (s *Service) ownedLatestVersion(ctx context.Context, tx pgx.Tx, actorID, scenarioID uuid.UUID, forUpdate bool) (ScenarioRecord, ScenarioVersionRecord, error) {
	lookup := s.store.ScenarioByID
	if forUpdate {
		lookup = s.store.ScenarioByIDForUpdate
	}
	sc, err := lookup(ctx, tx, scenarioID)
	if err != nil {
		return ScenarioRecord{}, ScenarioVersionRecord{}, err
	}
	if sc.CreatedBy != actorID {
		return ScenarioRecord{}, ScenarioVersionRecord{}, ErrNotFound
	}
	maxVersion, err := s.store.MaxVersion(ctx, tx, scenarioID)
	if err != nil {
		return ScenarioRecord{}, ScenarioVersionRecord{}, err
	}
	if maxVersion == 0 {
		return ScenarioRecord{}, ScenarioVersionRecord{}, ErrNotFound
	}
	v, err := s.store.VersionByNumber(ctx, tx, scenarioID, maxVersion)
	if err != nil {
		return ScenarioRecord{}, ScenarioVersionRecord{}, err
	}
	return sc, v, nil
}

// EditorScenarioDetail is the editor's own GET /scenarios/{id} — unlike
// read.go's ScenarioDetail (approved-only, no ownership check), this
// resolves the caller's own latest version regardless of status and
// 404s for anyone but its own author.
func (s *Service) EditorScenarioDetail(ctx context.Context, actorID, scenarioID uuid.UUID) (EditorScenario, error) {
	var result EditorScenario
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		sc, v, err := s.ownedLatestVersion(ctx, tx, actorID, scenarioID, false)
		if err != nil {
			return err
		}
		issues, err := ValidateDetailed(v.Body, storeCatalog{ctx: ctx, tx: tx, store: s.store})
		if err != nil {
			return err
		}
		result = EditorScenario{
			ScenarioRecord: sc, VersionID: v.ID, Version: v.Version, Status: v.Status,
			Body: v.Body, BodyJSON: v.BodyJSON, Digest: v.Digest, Issues: issues,
		}
		return nil
	})
	if err != nil {
		return EditorScenario{}, err
	}
	return result, nil
}

// ScenarioEditInput is PUT /scenarios/{id}'s own request
// (openapi.yaml's ScenarioEdit).
type ScenarioEditInput struct {
	// BaseVersionID and BaseDigestHex identify the version the client
	// last read. The digest alone covers only the body: a title-only save
	// creates a new version with the same digest, so a second tab still
	// holding the old digest would silently overwrite that title. Both
	// must match the scenario's latest version, or ErrStaleDraft.
	BaseVersionID uuid.UUID
	BaseDigestHex string
	Title         *string
	Difficulty    *int
	Body          Body
}

// SaveOperator112Draft is PUT /scenarios/{id}: always creates a new
// version (ADR-027's own simplification — no in-place edit), superseding
// whatever the scenario's previous latest version was. A structurally
// invalid body is still saved (Issues carries every problem;
// has_blocking_issues is only enforced by Preview/Approve, never Save).
func (s *Service) SaveOperator112Draft(ctx context.Context, actorID, scenarioID uuid.UUID, in ScenarioEditInput) (EditorScenario, error) {
	if !operator112EditorEligible(in.Body) {
		return EditorScenario{}, ErrUnsupportedForEditor
	}

	var result EditorScenario
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		sc, current, err := s.ownedLatestVersion(ctx, tx, actorID, scenarioID, true)
		if err != nil {
			return err
		}
		if current.ID != in.BaseVersionID || hex.EncodeToString(current.Digest[:]) != in.BaseDigestHex {
			return ErrStaleDraft
		}
		title := sc.Title
		if in.Title != nil {
			title = *in.Title
		}
		difficulty := sc.Difficulty
		if in.Difficulty != nil {
			difficulty = *in.Difficulty
		}
		body := in.Body
		body.Difficulty = difficulty
		_, canonicalJSON, digest, err := s.canonicalizeOperator112Body(body)
		if err != nil {
			return err
		}
		if _, err := s.store.SupersedeVersion(ctx, tx, current.ID); err != nil {
			return err
		}
		v, err := s.store.InsertScenarioVersion(ctx, tx, ScenarioVersionRecord{
			ID: uuid.New(), ScenarioID: scenarioID, Version: current.Version + 1, Status: "draft",
			Body: body, BodyJSON: canonicalJSON, Digest: digest, Difficulty: difficulty, CreatedBy: actorID,
		})
		if err != nil {
			return err
		}
		if title != sc.Title {
			if err := s.store.UpdateScenarioTitle(ctx, tx, scenarioID, title); err != nil {
				return err
			}
		}
		if difficulty != sc.Difficulty {
			if err := s.store.UpdateScenarioDifficulty(ctx, tx, scenarioID, difficulty); err != nil {
				return err
			}
		}
		issues, err := ValidateDetailed(body, storeCatalog{ctx: ctx, tx: tx, store: s.store})
		if err != nil {
			return err
		}
		if err := s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actorID, ActorRole: "instructor", Action: "scenario.save_draft",
			ResourceType: "scenario", ResourceID: &scenarioID, Outcome: audit.OutcomeOK,
		}); err != nil {
			return err
		}
		result = EditorScenario{
			ScenarioRecord: sc, VersionID: v.ID, Version: v.Version, Status: v.Status,
			Body: v.Body, BodyJSON: v.BodyJSON, Digest: v.Digest, Issues: issues,
		}
		result.ScenarioRecord.Title = title
		result.ScenarioRecord.Difficulty = difficulty
		return nil
	})
	if err != nil {
		return EditorScenario{}, err
	}
	return result, nil
}

// CheckPreviewable is POST /scenarios/{id}/preview-runs' own gate, run
// before training.StartPreview (which stops the author's previous
// preview and creates a real lesson): the *stored* version the request
// names — never an earlier client-side /validate of a form that may
// have changed since — must belong to the caller's own scenario and
// carry no Severity=error issue (BlockingIssuesError otherwise, the
// same rule approve enforces). StartPreview still re-checks ownership
// and status itself; this only adds the validation it cannot do.
func (s *Service) CheckPreviewable(ctx context.Context, actorID, scenarioID, versionID uuid.UUID) error {
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		sc, err := s.store.ScenarioByID(ctx, tx, scenarioID)
		if err != nil {
			return err
		}
		if sc.CreatedBy != actorID {
			return ErrNotFound
		}
		v, err := s.store.VersionByID(ctx, tx, versionID)
		if err != nil {
			return err
		}
		if v.ScenarioID != scenarioID {
			return ErrNotFound
		}
		issues, err := ValidateDetailed(v.Body, storeCatalog{ctx: ctx, tx: tx, store: s.store})
		if err != nil {
			return err
		}
		if hasBlockingIssue(issues) {
			return &BlockingIssuesError{Issues: issues}
		}
		return nil
	})
}

// ValidateOperator112Draft is POST /scenarios/{id}/validate: the
// in-progress editor form, checked without saving. 404s for anyone but
// the scenario's own author (same as every other editor entry point) —
// it does not need the scenario to have any particular version, only to
// exist and be owned, since it never reads the stored body at all.
func (s *Service) ValidateOperator112Draft(ctx context.Context, actorID, scenarioID uuid.UUID, body Body) ([]ValidationIssue, error) {
	var issues []ValidationIssue
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		sc, err := s.store.ScenarioByID(ctx, tx, scenarioID)
		if err != nil {
			return err
		}
		if sc.CreatedBy != actorID {
			return ErrNotFound
		}
		i, err := ValidateDetailed(body, storeCatalog{ctx: ctx, tx: tx, store: s.store})
		issues = i
		return err
	})
	if err != nil {
		return nil, err
	}
	return issues, nil
}

// ProbeMatch is POST /scenarios/{id}/probe's own response row.
type ProbeMatch struct {
	FactID string
	Kind   string // "reveal" or "ask"
}

// ProbeOperator112 runs text through the same deterministic classifier
// aicaller uses (AskedFacts/DiscloseReveals, intake_classify.go) against
// body's own facts — the editor's phrase tester, no model call. Reveals
// are computed as if every fact the operator could currently ask about
// is already open (OpenFacts' own "initial, or on_question once asked" —
// this endpoint has no transcript to derive "already asked" from, so it
// treats text as both the triggering question and, independently, a
// disclosure to check against every fact that isn't unknown — a author
// testing one phrase in isolation, not a real multi-turn conversation).
func (s *Service) ProbeOperator112(ctx context.Context, actorID, scenarioID uuid.UUID, body Body, text string) ([]ProbeMatch, error) {
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		sc, err := s.store.ScenarioByID(ctx, tx, scenarioID)
		if err != nil {
			return err
		}
		if sc.CreatedBy != actorID {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if body.Intake112 == nil || body.Intake112.Dialogue == nil {
		return nil, nil
	}
	facts := body.Intake112.Dialogue.Facts
	open := make(map[string]bool, len(facts))
	for _, fact := range facts {
		if fact.Knowledge == "initial" {
			open[fact.ID] = true
		}
	}
	var matches []ProbeMatch
	for _, id := range AskedFacts(facts, text) {
		matches = append(matches, ProbeMatch{FactID: id, Kind: "ask"})
		open[id] = true
	}
	for _, id := range DiscloseReveals(facts, open, text) {
		matches = append(matches, ProbeMatch{FactID: id, Kind: "reveal"})
	}
	return matches, nil
}

// ApproveOperator112Scenario is POST /scenarios/{id}/approve: rejects
// with *BlockingIssuesError if versionID (which must be scenarioID's own
// current draft, owned by actorID) still has an error-severity issue,
// otherwise supersedes the scenario's current approved version (if any)
// and approves versionID in the same transaction — the scenario's own
// status row (scenarios.status, otherwise only set once at creation)
// moves to "approved" alongside it.
func (s *Service) ApproveOperator112Scenario(ctx context.Context, actorID, scenarioID, versionID uuid.UUID, baseDigestHex string) (EditorScenario, error) {
	var result EditorScenario
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		sc, current, err := s.ownedLatestVersion(ctx, tx, actorID, scenarioID, true)
		if err != nil {
			return err
		}
		if current.ID != versionID {
			// An older version of this same scenario is a stale client
			// view (e.g. another tab saved since, even title-only), not a
			// missing resource.
			if v, err := s.store.VersionByID(ctx, tx, versionID); err == nil && v.ScenarioID == scenarioID {
				return ErrStaleDraft
			}
			return ErrNotFound
		}
		if current.Status != "draft" {
			return invalid("version_id", "not_draft")
		}
		if hex.EncodeToString(current.Digest[:]) != baseDigestHex {
			return ErrStaleDraft
		}
		if !operator112EditorEligible(current.Body) {
			return ErrUnsupportedForEditor
		}
		issues, err := ValidateDetailed(current.Body, storeCatalog{ctx: ctx, tx: tx, store: s.store})
		if err != nil {
			return err
		}
		if hasBlockingIssue(issues) {
			return &BlockingIssuesError{Issues: issues}
		}
		if _, err := s.store.SupersedeApprovedVersion(ctx, tx, scenarioID); err != nil {
			return err
		}
		found, err := s.store.ApproveVersion(ctx, tx, versionID, actorID)
		if err != nil {
			return err
		}
		if !found {
			return ErrStaleDraft
		}
		if err := s.store.UpdateScenarioStatus(ctx, tx, scenarioID, "approved"); err != nil {
			return err
		}
		if err := s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actorID, ActorRole: "instructor", Action: "scenario.approve",
			ResourceType: "scenario", ResourceID: &scenarioID, Outcome: audit.OutcomeOK,
		}); err != nil {
			return err
		}
		v, err := s.store.VersionByID(ctx, tx, versionID)
		if err != nil {
			return err
		}
		sc.Status = "approved"
		result = EditorScenario{ScenarioRecord: sc, VersionID: v.ID, Version: v.Version, Status: v.Status,
			Body: v.Body, BodyJSON: v.BodyJSON, Digest: v.Digest}
		return nil
	})
	if err != nil {
		return EditorScenario{}, err
	}
	return result, nil
}

// IntakeCatalogForInstructor is GET /intake112/catalog — the same
// catalog already loaded into every 112 item's own intake_state, just
// gathered for the editor rather than a running item.
func (s *Service) IntakeCatalogForInstructor(ctx context.Context) (IntakeCatalog, error) {
	var catalog IntakeCatalog
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		c, err := s.store.LatestIntakeCatalog(ctx, tx)
		catalog = c
		return err
	})
	if err != nil {
		return IntakeCatalog{}, err
	}
	return catalog, nil
}

// canonicalizeOperator112Body prepares an editor-authored Body for
// storage: it fills in the DDS-only array fields (Hints/Contacts/Events)
// with empty, non-nil slices when unset — Body's own json tags have no
// omitempty (BodyJSON's doc comment), so json.Marshal(body) would
// otherwise emit an explicit "hints": null etc. for a field the 112
// editor never touches — before re-encoding through DecodeBody so
// raw/canonicalJSON/digest are all derived from that same JSON shape,
// never a direct marshal of the typed struct (matching how a file
// import's own digest is computed, digest.go's own doc comment).
//
// This does not run scenario.schema.json's own JSON-Schema validation
// (schema.Validator.ValidateFile): that schema's card/reference branch
// assumes a DDS body's Card/Reference are entirely *absent* for
// operator112_intake, which Go's Body struct (Card/Reference are
// non-pointer, non-omitempty — every DDS scenario needs them present)
// cannot represent by construction; reshaping Body to make Card/
// Reference optional is a larger, DDS-affecting change outside 112-7's
// scope. The editor therefore relies on ValidateDetailed's semantic
// checks alone, the same way it already carries every other
// authoring-time problem as an Issue rather than a schema error.
func (s *Service) canonicalizeOperator112Body(body Body) (raw any, canonicalJSON []byte, digest [32]byte, err error) {
	if body.Hints == nil {
		body.Hints = []Hint{}
	}
	if body.Contacts == nil {
		body.Contacts = []Contact{}
	}
	if body.Events == nil {
		body.Events = []Event{}
	}
	marshaled, err := json.Marshal(body)
	if err != nil {
		return nil, nil, [32]byte{}, err
	}
	raw, _, err = DecodeBody(bytes.NewReader(marshaled))
	if err != nil {
		return nil, nil, [32]byte{}, err
	}
	canonicalJSON, err = json.Marshal(raw)
	if err != nil {
		return nil, nil, [32]byte{}, err
	}
	digest = Digest(raw)
	return raw, canonicalJSON, digest, nil
}
