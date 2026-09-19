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
	if servicesResult.Created != 3 || servicesResult.Unchanged != 0 {
		t.Fatalf("ImportServices = %+v, want Created=3 Unchanged=0", servicesResult)
	}
	servicesResult2, err := svc.ImportServices(ctx, openSeedFile(t, "../../seed/services.json"), actorID, actorRole, "req-2")
	if err != nil {
		t.Fatalf("ImportServices (replay): %v", err)
	}
	if servicesResult2.Created != 0 || servicesResult2.Unchanged != 3 {
		t.Fatalf("ImportServices (replay) = %+v, want Created=0 Unchanged=3", servicesResult2)
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
	if scenarioResult.NewScenarios != 2 || scenarioResult.NewVersions != 2 || scenarioResult.Unchanged != 0 {
		t.Fatalf("ImportScenarios = %+v, want NewScenarios=2 NewVersions=2 Unchanged=0", scenarioResult)
	}
	scenarioResult2, err := svc.ImportScenarios(ctx, openScenarioDir(t, "../../seed/scenarios"), actorID, actorRole, "req-6")
	if err != nil {
		t.Fatalf("ImportScenarios (replay): %v", err)
	}
	if scenarioResult2.NewScenarios != 0 || scenarioResult2.NewVersions != 0 || scenarioResult2.Unchanged != 2 {
		t.Fatalf("ImportScenarios (replay) = %+v, want all Unchanged=2", scenarioResult2)
	}

	// --- read side ---
	items, total, err := svc.ListScenarios(ctx, content.ScenarioFilter{})
	if err != nil {
		t.Fatalf("ListScenarios: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("ListScenarios: total=%d len=%d, want 2 and 2", total, len(items))
	}

	var case02ID uuid.UUID
	for _, it := range items {
		if it.SourceKey != nil && *it.SourceKey == "pilot-tree-02" {
			case02ID = it.ID
		}
	}
	if case02ID == uuid.Nil {
		t.Fatalf("pilot-tree-02 not found in ListScenarios: %+v", items)
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
