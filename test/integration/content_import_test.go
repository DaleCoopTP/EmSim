// New test: internal/content end to end against real PostgreSQL — the
// real seed/ files (slice-2-plan.md's C3) load through
// internal/content.Service exactly as `emsim import seed` would, a
// second run is idempotent, a conflicting re-import is rejected without
// writing anything, and the catalogue read side (list/detail/versions/
// preview) reflects what was written. content_fk_test.go covers the
// users_service_code_fkey NOT VALID -> validated transition separately.
//
//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	"emsim/internal/content/schema"
	pgstore "emsim/internal/platform/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// mustValidator compiles the scenario schemas once per test.
func mustValidator(t *testing.T) *schema.Validator {
	t.Helper()
	v, err := schema.New()
	if err != nil {
		t.Fatalf("schema.New: %v", err)
	}
	return v
}

// createContentAdmin inserts an active admin the way cmd/emsim/import.go's
// resolveImportActor expects to find one, and returns its id/role.
func createContentAdmin(t *testing.T, ctx context.Context, store *authpg.Store, login string) (uuid.UUID, string) {
	t.Helper()
	var id uuid.UUID
	err := store.WithTx(ctx, func(tx pgx.Tx) error {
		u, err := store.InsertUser(ctx, tx, newAdmin(login))
		id = u.ID
		return err
	})
	if err != nil {
		t.Fatalf("insert admin %q: %v", login, err)
	}
	return id, string(auth.RoleAdmin)
}

func openSeedFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// openScenarioDir opens every *.json file in dir, keyed by filename, the
// way cmd/emsim/import.go's importScenariosStep does.
func openScenarioDir(t *testing.T, dir string) map[string]io.Reader {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	files := make(map[string]io.Reader, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		files[entry.Name()] = openSeedFile(t, filepath.Join(dir, entry.Name()))
	}
	return files
}

func TestContentImportSeedEndToEnd(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	pool, err := pgstore.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	authStore := authpg.NewStore(pool)
	actorID, actorRole := createContentAdmin(t, ctx, authStore, "content-admin")

	svc := content.NewService(contentpg.NewStore(pool), mustValidator(t))

	// --- services ---
	servicesResult, err := svc.ImportServices(ctx, openSeedFile(t, "../../seed/services.json"), actorID, actorRole, "req-1")
	if err != nil {
		t.Fatalf("ImportServices: %v", err)
	}
	if servicesResult.Created != 6 || servicesResult.Unchanged != 0 {
		t.Fatalf("ImportServices = %+v, want Created=6 Unchanged=0", servicesResult)
	}
	servicesResult2, err := svc.ImportServices(ctx, openSeedFile(t, "../../seed/services.json"), actorID, actorRole, "req-2")
	if err != nil {
		t.Fatalf("ImportServices (replay): %v", err)
	}
	if servicesResult2.Created != 0 || servicesResult2.Unchanged != 6 {
		t.Fatalf("ImportServices (replay) = %+v, want Created=0 Unchanged=6", servicesResult2)
	}
	intakeCatalog, err := svc.ImportIntakeCatalog(ctx, openSeedFile(t, "../../seed/intake-catalog.json"), actorID, actorRole, "intake-catalog-1")
	if err != nil || intakeCatalog.Created != 1 {
		t.Fatalf("ImportIntakeCatalog = %+v, %v", intakeCatalog, err)
	}
	intakeCatalog, err = svc.ImportIntakeCatalog(ctx, openSeedFile(t, "../../seed/intake-catalog.json"), actorID, actorRole, "intake-catalog-2")
	if err != nil || intakeCatalog.Unchanged != 1 {
		t.Fatalf("ImportIntakeCatalog replay = %+v, %v", intakeCatalog, err)
	}

	// --- classifier ---
	classifierResult, err := svc.ImportClassifierTypes(ctx, openSeedFile(t, "../../seed/classifier.json"), actorID, actorRole, "req-3")
	if err != nil {
		t.Fatalf("ImportClassifierTypes: %v", err)
	}
	if classifierResult.Created != 1 || classifierResult.Unchanged != 0 {
		t.Fatalf("ImportClassifierTypes = %+v, want Created=1 Unchanged=0", classifierResult)
	}
	classifierResult2, err := svc.ImportClassifierTypes(ctx, openSeedFile(t, "../../seed/classifier.json"), actorID, actorRole, "req-4")
	if err != nil {
		t.Fatalf("ImportClassifierTypes (replay): %v", err)
	}
	if classifierResult2.Created != 0 || classifierResult2.Unchanged != 1 {
		t.Fatalf("ImportClassifierTypes (replay) = %+v, want Created=0 Unchanged=1", classifierResult2)
	}

	// --- scenarios ---
	scenarioResult, err := svc.ImportScenarios(ctx, openScenarioDir(t, "../../seed/scenarios"), actorID, actorRole, "req-5")
	if err != nil {
		t.Fatalf("ImportScenarios: %v", err)
	}
	if scenarioResult.NewScenarios != 11 || scenarioResult.NewVersions != 12 || scenarioResult.Unchanged != 0 {
		t.Fatalf("ImportScenarios = %+v, want NewScenarios=11 NewVersions=12 Unchanged=0", scenarioResult)
	}
	scenarioResult2, err := svc.ImportScenarios(ctx, openScenarioDir(t, "../../seed/scenarios"), actorID, actorRole, "req-6")
	if err != nil {
		t.Fatalf("ImportScenarios (replay): %v", err)
	}
	if scenarioResult2.NewScenarios != 0 || scenarioResult2.NewVersions != 0 || scenarioResult2.Unchanged != 12 {
		t.Fatalf("ImportScenarios (replay) = %+v, want all Unchanged=12", scenarioResult2)
	}

	// --- read side ---
	items, total, err := svc.ListScenarios(ctx, content.ScenarioFilter{})
	if err != nil {
		t.Fatalf("ListScenarios: %v", err)
	}
	if total != 11 || len(items) != 11 {
		t.Fatalf("ListScenarios: total=%d len=%d, want 11 including three card-only cases and one full_case", total, len(items))
	}

	var case02ID uuid.UUID
	var intakeID uuid.UUID
	for _, it := range items {
		if it.SourceKey != nil && *it.SourceKey == "pilot-tree-02" {
			case02ID = it.ID
		}
		if it.SourceKey != nil && *it.SourceKey == "pilot-112-medical-01" {
			intakeID = it.ID
			if it.ExerciseType != content.ExerciseTypeOperator112Intake || it.TargetService != "" {
				t.Fatalf("112 catalogue entry has wrong type/target: %+v", it)
			}
		}
	}
	if case02ID == uuid.Nil {
		t.Fatalf("pilot-tree-02 not found in ListScenarios: %+v", items)
	}
	if intakeID == uuid.Nil {
		t.Fatalf("pilot-112-medical-01 not found in ListScenarios: %+v", items)
	}
	intakeItems, intakeTotal, err := svc.ListScenarios(ctx, content.ScenarioFilter{ExerciseType: content.ExerciseTypeOperator112Intake})
	if err != nil || intakeTotal != 7 || len(intakeItems) != 7 {
		t.Fatalf("112 catalogue filter: items=%+v total=%d err=%v", intakeItems, intakeTotal, err)
	}
	intakeDetail, err := svc.ScenarioDetail(ctx, intakeID)
	if err != nil || intakeDetail.Body.Intake112 == nil || intakeDetail.Body.Intake112.Call == nil || intakeDetail.Body.Intake112.Call.LocalTime != "02:03" || intakeDetail.Body.Intake112.Dialogue == nil {
		t.Fatalf("112 scenario detail: detail=%+v err=%v", intakeDetail, err)
	}
	var storedExerciseType string
	if err := pool.QueryRow(ctx, `SELECT exercise_type FROM scenario_versions WHERE id=$1`, intakeDetail.VersionID).Scan(&storedExerciseType); err != nil || storedExerciseType != "operator112_intake" {
		t.Fatalf("stored 112 exercise_type = %q, err=%v", storedExerciseType, err)
	}

	detail, err := svc.ScenarioDetail(ctx, case02ID)
	if err != nil {
		t.Fatalf("ScenarioDetail: %v", err)
	}
	if detail.Body.Card.Address.Okrug != "ЮАР" {
		t.Fatalf("pilot-tree-02 card.address.okrug = %q, want ЮАР (the deliberate error)", detail.Body.Card.Address.Okrug)
	}
	if len(detail.Body.Reference.FieldCorrections) != 1 || detail.Body.Reference.FieldCorrections[0].ExpectedValue != "ЮАО" {
		t.Fatalf("pilot-tree-02 field_corrections = %+v, want one entry with expected_value ЮАО", detail.Body.Reference.FieldCorrections)
	}

	// The stored/served body must reproduce its own digest. jsonb
	// reformats on output (e.g. ": "/", " spacing), so this decodes what
	// came back and re-canonicalizes it — content.Canonical is a pure
	// function of the JSON *value*, so this is byte-identical to what
	// content.Digest hashed at import time as long as the stored body is
	// the same value, unlike a re-marshaled typed Body (which, having no
	// omitempty, would turn the seed file's omitted "hints"/"generation"
	// into explicit nulls neither the digest nor scenario.schema.json
	// would accept).
	var storedRaw any
	dec := json.NewDecoder(bytes.NewReader(detail.BodyJSON))
	dec.UseNumber()
	if err := dec.Decode(&storedRaw); err != nil {
		t.Fatalf("decode stored body: %v", err)
	}
	if got := content.Digest(storedRaw); got != detail.Digest {
		t.Fatalf("content.Digest(decoded stored body) = %x, want digest %x", got, detail.Digest)
	}
	if strings.Contains(string(detail.BodyJSON), `"hints"`) || strings.Contains(string(detail.BodyJSON), `"generation"`) {
		t.Fatalf("stored body must omit fields the seed file itself omits, got: %s", detail.BodyJSON)
	}

	versions, err := svc.ScenarioVersions(ctx, case02ID)
	if err != nil {
		t.Fatalf("ScenarioVersions: %v", err)
	}
	if len(versions) != 1 || versions[0].Status != "approved" || versions[0].Version != 1 {
		t.Fatalf("ScenarioVersions = %+v, want one approved v1", versions)
	}

	preview, err := svc.ScenarioPreview(ctx, case02ID)
	if err != nil {
		t.Fatalf("ScenarioPreview: %v", err)
	}
	if preview.Card.Address.Okrug != "ЮАР" {
		t.Fatalf("preview.Card.Address.Okrug = %q, want ЮАР (preview shows the card as-is, not corrected)", preview.Card.Address.Okrug)
	}
	if len(preview.Reference.FieldCorrections) != 1 || preview.Reference.FieldCorrections[0].ExpectedValue != "ЮАО" {
		t.Fatalf("preview.Reference.FieldCorrections not carried through: %+v", preview.Reference.FieldCorrections)
	}

	// PostgreSQL jsonb expands exponent notation on storage (1e2 -> 100).
	// The canonical number form must do the same so the persisted body still
	// reproduces the digest calculated before INSERT.
	seedServiceAndClassifier(t, ctx, svc, actorID, actorRole)
	numericScenario := strings.Replace(
		scenarioFileJSON(t, "numeric-jsonb", 1, "Numeric JSONB", "accepted"),
		`"features": {}`,
		`"features": {"reading": 1e2}`,
		1,
	)
	if _, err := svc.ImportScenarios(ctx, map[string]io.Reader{"numeric.json": strings.NewReader(numericScenario)}, actorID, actorRole, "req-numeric"); err != nil {
		t.Fatalf("ImportScenarios(numeric): %v", err)
	}
	var numericID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM scenarios WHERE source_key = 'numeric-jsonb'`).Scan(&numericID); err != nil {
		t.Fatalf("find numeric scenario: %v", err)
	}
	numericDetail, err := svc.ScenarioDetail(ctx, numericID)
	if err != nil {
		t.Fatalf("ScenarioDetail(numeric): %v", err)
	}
	var numericRaw any
	numericDecoder := json.NewDecoder(bytes.NewReader(numericDetail.BodyJSON))
	numericDecoder.UseNumber()
	if err := numericDecoder.Decode(&numericRaw); err != nil {
		t.Fatalf("decode stored numeric body: %v", err)
	}
	if got := content.Digest(numericRaw); got != numericDetail.Digest {
		t.Fatalf("digest after jsonb number normalization = %x, want %x (body=%s)", got, numericDetail.Digest, numericDetail.BodyJSON)
	}
	for _, literal := range []string{`1e2`, `1.230e-5`, `0.001e1`, `123.4500`, `-12.5e+2`, `-0.00e+10`} {
		var jsonbText string
		if err := pool.QueryRow(ctx, `SELECT ($1::text)::jsonb::text`, literal).Scan(&jsonbText); err != nil {
			t.Fatalf("render %s through jsonb: %v", literal, err)
		}
		decoder := json.NewDecoder(strings.NewReader(literal))
		decoder.UseNumber()
		var rawNumber any
		if err := decoder.Decode(&rawNumber); err != nil {
			t.Fatalf("decode number %s: %v", literal, err)
		}
		if canonical := string(content.Canonical(rawNumber)); canonical != jsonbText {
			t.Fatalf("Canonical(%s) = %s, PostgreSQL jsonb renders %s", literal, canonical, jsonbText)
		}
	}
}

// TestContentImportConflicts checks that a re-import disagreeing with
// what is already stored is rejected (ErrConflict) and writes nothing,
// covering services, classifier types and scenario versions.
func TestContentImportConflicts(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	pool, err := pgstore.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	authStore := authpg.NewStore(pool)
	actorID, actorRole := createContentAdmin(t, ctx, authStore, "conflict-admin")
	svc := content.NewService(contentpg.NewStore(pool), mustValidator(t))

	const original = `[{"code":"svc_a","name":"Служба А","workflow":{"transitions":{"added":["received"]}}}]`
	if _, err := svc.ImportServices(ctx, strings.NewReader(original), actorID, actorRole, "r1"); err != nil {
		t.Fatalf("seed ImportServices: %v", err)
	}

	const changedName = `[{"code":"svc_a","name":"Другое название","workflow":{"transitions":{"added":["received"]}}}]`
	_, err = svc.ImportServices(ctx, strings.NewReader(changedName), actorID, actorRole, "r2")
	var conflictErr *content.ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("ImportServices(changed name) error = %v, want *content.ConflictError", err)
	}

	// the conflicting attempt must not have written anything
	services, err := svc.ListServices(ctx)
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(services) != 1 || services[0].Name != "Служба А" {
		t.Fatalf("ListServices after rejected conflict = %+v, want unchanged single entry", services)
	}

	const classifierOriginal = `[{"code":"c1","name":"Тип 1","features":{},"notify":["svc_a"]}]`
	if _, err := svc.ImportClassifierTypes(ctx, strings.NewReader(classifierOriginal), actorID, actorRole, "r3"); err != nil {
		t.Fatalf("seed ImportClassifierTypes: %v", err)
	}
	const classifierChanged = `[{"code":"c1","name":"Другой тип","features":{},"notify":["svc_a"]}]`
	_, err = svc.ImportClassifierTypes(ctx, strings.NewReader(classifierChanged), actorID, actorRole, "r4")
	if !errors.As(err, &conflictErr) {
		t.Fatalf("ImportClassifierTypes(changed name) error = %v, want *content.ConflictError", err)
	}
}

// TestContentImportScenarioVersionRules exercises
// content.Service.ImportScenarios' version bookkeeping directly against
// PostgreSQL: a legitimate v2 supersedes v1 while preserving its
// approval attribution, redefining an existing version's content is
// rejected, and an invalid file in a batch rolls back the whole batch —
// the scenario the valid file in the same batch would have created does
// not appear either.
func TestContentImportScenarioVersionRules(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	pool, err := pgstore.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	authStore := authpg.NewStore(pool)
	actorID, actorRole := createContentAdmin(t, ctx, authStore, "version-admin")
	svc := content.NewService(contentpg.NewStore(pool), mustValidator(t))

	seedServiceAndClassifier(t, ctx, svc, actorID, actorRole)
	unknownSpawn := strings.Replace(
		scenarioFileJSON(t, "unknown-spawn", 1, "Unknown spawn", "accepted"),
		`"events": []`,
		`"events": [{"key":"e1","at_s":0,"since":"accepted","delivery":"spawn_card","spawn":{"kind":"scenario","scenario_key":"missing-target","version":1}}]`,
		1,
	)
	_, err = svc.ImportScenarios(ctx, map[string]io.Reader{"unknown.json": strings.NewReader(unknownSpawn)}, actorID, actorRole, "r0")
	var validationErr *content.ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "events[0].spawn" {
		t.Fatalf("import with unknown spawn version error = %v, want stable-reference validation error", err)
	}

	v1 := scenarioFileJSON(t, "vtest", 1, "V1 title", "accepted")
	if _, err := svc.ImportScenarios(ctx, map[string]io.Reader{"v1.json": strings.NewReader(v1)}, actorID, actorRole, "r1"); err != nil {
		t.Fatalf("import v1: %v", err)
	}

	items, _, err := svc.ListScenarios(ctx, content.ScenarioFilter{})
	if err != nil {
		t.Fatalf("ListScenarios: %v", err)
	}
	var scenarioID uuid.UUID
	for _, it := range items {
		if it.SourceKey != nil && *it.SourceKey == "vtest" {
			scenarioID = it.ID
		}
	}
	if scenarioID == uuid.Nil {
		t.Fatalf("vtest scenario not found after v1 import")
	}
	v1Versions, err := svc.ScenarioVersions(ctx, scenarioID)
	if err != nil {
		t.Fatalf("ScenarioVersions after v1: %v", err)
	}
	if len(v1Versions) != 1 || v1Versions[0].ApprovedBy == nil {
		t.Fatalf("v1 not approved: %+v", v1Versions)
	}
	v1ApprovedBy := *v1Versions[0].ApprovedBy
	v1ApprovedAt := *v1Versions[0].ApprovedAt

	compatibleSpawn := strings.Replace(
		scenarioFileJSON(t, "compatible-spawn", 1, "Compatible spawn", "accepted"),
		`"events": []`,
		`"events": [{"key":"e1","at_s":0,"since":"accepted","delivery":"spawn_card","spawn":{"kind":"scenario","scenario_key":"vtest","version":1}}]`,
		1,
	)
	if _, err := svc.ImportScenarios(ctx, map[string]io.Reader{"compatible.json": strings.NewReader(compatibleSpawn)}, actorID, actorRole, "r1b"); err != nil {
		t.Fatalf("import with compatible spawn version: %v", err)
	}

	// The referring key sorts before its target, so this only succeeds if
	// ImportScenarios overlays the decoded batch while validating stable
	// spawn references instead of depending on filename/import order.
	batchTarget := scenarioFileJSON(t, "z-batch-target", 1, "Batch target", "accepted")
	batchRef := strings.Replace(
		scenarioFileJSON(t, "a-batch-ref", 1, "Batch reference", "accepted"),
		`"events": []`,
		`"events": [{"key":"e1","at_s":0,"since":"accepted","delivery":"spawn_card","spawn":{"kind":"scenario","scenario_key":"z-batch-target","version":1}}]`,
		1,
	)
	if _, err := svc.ImportScenarios(ctx, map[string]io.Reader{
		"target.json": strings.NewReader(batchTarget), "reference.json": strings.NewReader(batchRef),
	}, actorID, actorRole, "r1c"); err != nil {
		t.Fatalf("import batch with order-independent spawn reference: %v", err)
	}

	// v2 supersedes v1
	v2 := scenarioFileJSON(t, "vtest", 2, "V1 title", "not_accepted")
	if _, err := svc.ImportScenarios(ctx, map[string]io.Reader{"v2.json": strings.NewReader(v2)}, actorID, actorRole, "r2"); err != nil {
		t.Fatalf("import v2: %v", err)
	}
	versions, err := svc.ScenarioVersions(ctx, scenarioID)
	if err != nil {
		t.Fatalf("ScenarioVersions after v2: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions after v2 = %+v, want 2", versions)
	}
	for _, v := range versions {
		switch v.Version {
		case 1:
			if v.Status != "superseded" {
				t.Fatalf("v1.status = %q, want superseded", v.Status)
			}
			if v.ApprovedBy == nil || *v.ApprovedBy != v1ApprovedBy || v.ApprovedAt == nil || !v.ApprovedAt.Equal(v1ApprovedAt) {
				t.Fatalf("v1 approval attribution not preserved after supersede: %+v", v)
			}
		case 2:
			if v.Status != "approved" {
				t.Fatalf("v2.status = %q, want approved", v.Status)
			}
		}
	}

	// Replaying the whole directory — including v1.json, whose version is
	// now superseded — must stay idempotent: a historical version with an
	// unchanged digest is a no-op, not version_regression, so re-running
	// `import scenarios` on a seed directory after a new version has been
	// added (the ordinary "docker compose up" / re-seed path) does not fail.
	replay, err := svc.ImportScenarios(ctx, map[string]io.Reader{"v1.json": strings.NewReader(v1), "v2.json": strings.NewReader(v2)}, actorID, actorRole, "r2b")
	if err != nil {
		t.Fatalf("replaying v1+v2 after v2 superseded v1: %v", err)
	}
	if replay.NewScenarios != 0 || replay.NewVersions != 0 || replay.Unchanged != 2 {
		t.Fatalf("replay of v1+v2 = %+v, want all Unchanged=2", replay)
	}
	versionsAfterReplay, err := svc.ScenarioVersions(ctx, scenarioID)
	if err != nil {
		t.Fatalf("ScenarioVersions after replay: %v", err)
	}
	if len(versionsAfterReplay) != 2 {
		t.Fatalf("versions after replay = %+v, want 2", versionsAfterReplay)
	}
	for _, v := range versionsAfterReplay {
		switch v.Version {
		case 1:
			if v.Status != "superseded" {
				t.Fatalf("v1.status after replay = %q, want still superseded", v.Status)
			}
		case 2:
			if v.Status != "approved" {
				t.Fatalf("v2.status after replay = %q, want still approved", v.Status)
			}
		}
	}

	// redefining v2 with different content is rejected
	v2Redefined := scenarioFileJSON(t, "vtest", 2, "V1 title", "accepted")
	_, err = svc.ImportScenarios(ctx, map[string]io.Reader{"v2b.json": strings.NewReader(v2Redefined)}, actorID, actorRole, "r3")
	var conflictErr *content.ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("re-importing v2 with different content: error = %v, want *content.ConflictError", err)
	}

	// a batch with one valid new scenario and one invalid file rolls back entirely
	validNew := scenarioFileJSON(t, "vtest-batch", 1, "Batch title", "accepted")
	invalid := `{"schema":"emsim/scenario-file/v1","key":"bad","version":1,"title":"t","origin":"manual","body":{}}`
	_, err = svc.ImportScenarios(ctx, map[string]io.Reader{
		"valid.json":   strings.NewReader(validNew),
		"invalid.json": strings.NewReader(invalid),
	}, actorID, actorRole, "r4")
	if err == nil {
		t.Fatalf("ImportScenarios(batch with an invalid file) should fail")
	}
	itemsAfter, _, err := svc.ListScenarios(ctx, content.ScenarioFilter{})
	if err != nil {
		t.Fatalf("ListScenarios after rolled-back batch: %v", err)
	}
	for _, it := range itemsAfter {
		if it.SourceKey != nil && *it.SourceKey == "vtest-batch" {
			t.Fatalf("vtest-batch was persisted despite the batch containing an invalid file")
		}
	}

	// A full history is applied by (scenario key, numeric version), not by
	// filename. Lexicographic filename order here is v1, v10, v2... and
	// used to fail at v10 with version_gap before v2 was processed.
	history := make(map[string]io.Reader, 10)
	for version := 1; version <= 10; version++ {
		status := "accepted"
		if version%2 == 0 {
			status = "not_accepted"
		}
		name := "history-v" + strconv.Itoa(version) + ".json"
		history[name] = strings.NewReader(scenarioFileJSON(t, "history-order", version, "History order", status))
	}
	historyResult, err := svc.ImportScenarios(ctx, history, actorID, actorRole, "r5")
	if err != nil {
		t.Fatalf("import lexicographically misordered history: %v", err)
	}
	if historyResult.NewScenarios != 1 || historyResult.NewVersions != 10 || historyResult.Unchanged != 0 {
		t.Fatalf("history import = %+v, want one scenario and ten versions", historyResult)
	}
}

func TestScenarioVersionLifecycleCannotBeRewoundOrReattributed(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	pool, err := pgstore.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	if err := pgstore.UpTo(ctx, databaseURL, 4); err != nil {
		t.Fatalf("migrate up to vulnerable schema version 4: %v", err)
	}

	authStore := authpg.NewStore(pool)
	actorID, actorRole := createContentAdmin(t, ctx, authStore, "lifecycle-admin")
	otherAdminID, _ := createContentAdmin(t, ctx, authStore, "other-lifecycle-admin")
	svc := content.NewService(contentpg.NewStore(pool), mustValidator(t))
	seedServiceAndClassifier(t, ctx, svc, actorID, actorRole)

	v1 := scenarioFileJSON(t, "lifecycle", 1, "Lifecycle", "accepted")
	if _, err := svc.ImportScenarios(ctx, map[string]io.Reader{"v1.json": strings.NewReader(v1)}, actorID, actorRole, "r1"); err != nil {
		t.Fatalf("import v1: %v", err)
	}

	var scenarioID, v1ID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT s.id, sv.id
		FROM scenarios s
		JOIN scenario_versions sv ON sv.scenario_id = s.id
		WHERE s.source_key = 'lifecycle' AND sv.version = 1
	`).Scan(&scenarioID, &v1ID); err != nil {
		t.Fatalf("find imported v1: %v", err)
	}
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatalf("apply lifecycle migration: %v", err)
	}

	// This was the deletion bypass: clear attribution while rewinding an
	// approved row to draft, then delete the now-unprotected draft.
	if _, err := pool.Exec(ctx, `
		UPDATE scenario_versions
		SET status = 'draft', approved_by = NULL, approved_at = NULL
		WHERE id = $1
	`, v1ID); err == nil {
		t.Fatal("approved -> draft rewind unexpectedly succeeded")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM scenario_versions WHERE id = $1`, v1ID); err == nil {
		t.Fatal("deleting an approved version unexpectedly succeeded after rejected rewind")
	}
	if _, err := pool.Exec(ctx, `UPDATE scenario_versions SET approved_by = $2 WHERE id = $1`, v1ID, otherAdminID); err == nil {
		t.Fatal("changing saved approval attribution unexpectedly succeeded")
	}

	v2 := scenarioFileJSON(t, "lifecycle", 2, "Lifecycle", "not_accepted")
	if _, err := svc.ImportScenarios(ctx, map[string]io.Reader{"v2.json": strings.NewReader(v2)}, actorID, actorRole, "r2"); err != nil {
		t.Fatalf("import v2: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE scenario_versions
		SET status = 'draft', approved_by = NULL, approved_at = NULL
		WHERE id = $1
	`, v1ID); err == nil {
		t.Fatal("superseded -> draft rewind unexpectedly succeeded")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM scenario_versions WHERE id = $1`, v1ID); err == nil {
		t.Fatal("deleting a superseded version unexpectedly succeeded")
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM scenario_versions WHERE id = $1 AND scenario_id = $2`, v1ID, scenarioID).Scan(&status); err != nil {
		t.Fatalf("read protected v1: %v", err)
	}
	if status != "superseded" {
		t.Fatalf("v1.status = %q, want superseded", status)
	}
}

func seedServiceAndClassifier(t *testing.T, ctx context.Context, svc *content.Service, actorID uuid.UUID, actorRole string) {
	t.Helper()
	const services = `[{"code":"svc_a","name":"Служба А","workflow":{"transitions":{"added":["received"],"received":["accepted","not_accepted"],"not_accepted":["accepted"]}}}]`
	if _, err := svc.ImportServices(ctx, strings.NewReader(services), actorID, actorRole, "seed-svc"); err != nil {
		t.Fatalf("seed services: %v", err)
	}
	const classifier = `[{"code":"c1","name":"Тип 1","features":{},"notify":["svc_a"]}]`
	if _, err := svc.ImportClassifierTypes(ctx, strings.NewReader(classifier), actorID, actorRole, "seed-cls"); err != nil {
		t.Fatalf("seed classifier: %v", err)
	}
}

// scenarioFileJSON builds a minimal, always schema- and
// semantically-valid scenario file for key/version/title/
// primary_decision.status against seedServiceAndClassifier's fixtures.
func scenarioFileJSON(t *testing.T, key string, version int, title, primaryStatus string) string {
	t.Helper()
	return `{
		"schema": "emsim/scenario-file/v1",
		"key": "` + key + `",
		"version": ` + strconv.Itoa(version) + `,
		"title": "` + title + `",
		"origin": "manual",
		"body": {
			"schema": "emsim/scenario/v1",
			"target_service": "svc_a",
			"card": {
				"number": "123456",
				"registered_at_offset_s": -30,
				"applicant": {"name": "T", "phone": "+79161234567"},
				"address": {"district": "d", "street": "s", "house": "1"},
				"incident": {"type_code": "c1", "type_name": "Тип 1", "features": {}, "description": "Учебное описание длиной более двадцати символов."},
				"notification_list": [{"service": "svc_a", "status": "added", "mine": true}]
			},
			"contacts": [],
			"events": [],
			"reference": {
				"primary_decision": {"status": "` + primaryStatus + `"},
				"expected_chain": [],
				"call": {"required": false}
			},
			"difficulty": 1,
			"exercise_type": "dds_processing"
		}
	}`
}
