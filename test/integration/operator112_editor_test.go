// 112-7/ADR-027's own integration tests (slice-112-7-plan.md's c6): the
// editor's create/save/approve version lifecycle against a real
// PostgreSQL database (digest-based optimistic locking, ownership,
// version superseding — none of that is exercised by internal/content's
// own unit tests, which use an in-memory fake store), and
// training.Service.StartPreview end to end: a real preview lesson/run/
// item with no workstation, drivable by its instructor author exactly
// like a trainee drives their own item, closing into a real rubric-v2
// auto-assessment that is nonetheless invisible to every reporting/
// listing/recommendation surface.
//
//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"emsim/internal/assessment"
	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	pgstore "emsim/internal/platform/postgres"
	"emsim/internal/training"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestOperator112EditorVersionLifecycleAgainstRealDatabase drives
// content.Service's editor methods (CreateOperator112Scenario,
// SaveOperator112Draft, ApproveOperator112Scenario) against a real
// database: every PUT creates a new version and supersedes the old one
// (ADR-027's simplification of the original "edit in place until
// referenced" design, chosen to avoid content depending on training),
// base_digest mismatches are rejected as stale, and a foreign instructor
// can neither see nor edit another author's draft but can copy their
// approved version into a new scenario under their own authorship.
func TestOperator112EditorVersionLifecycleAgainstRealDatabase(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	authStore := authpg.NewStore(pool)
	author := insertInstructor(t, ctx, pool, "editor-author-"+uuid.NewString())
	other := insertInstructor(t, ctx, pool, "editor-other-"+uuid.NewString())
	adminID, adminRole := createContentAdmin(t, ctx, authStore, "editor-admin-"+uuid.NewString())
	contentService := content.NewService(contentpg.NewStore(pool), mustValidator(t))
	if _, err := contentService.ImportServices(ctx, openSeedFile(t, "../../seed/services.json"), adminID, adminRole, "editor-services"); err != nil {
		t.Fatal(err)
	}
	if _, err := contentService.ImportIntakeCatalog(ctx, openSeedFile(t, "../../seed/intake-catalog.json"), adminID, adminRole, "editor-catalog"); err != nil {
		t.Fatal(err)
	}

	body := unmarshalScenarioBody(t, freeTextChatScenarioJSON)

	created, err := contentService.CreateOperator112Scenario(ctx, author.ID, content.ScenarioCreateInput{Title: "Мой кейс", Difficulty: 1, Body: &body})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Version != 1 || created.Status != "draft" || created.CreatedBy != author.ID {
		t.Fatalf("created = %+v, want version=1 status=draft created_by=author", created)
	}

	// A foreign instructor cannot see this draft at all.
	if _, err := contentService.EditorScenarioDetail(ctx, other.ID, created.ID); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("foreign EditorScenarioDetail = %v, want ErrNotFound", err)
	}
	if _, err := contentService.SaveOperator112Draft(ctx, other.ID, created.ID, content.ScenarioEditInput{BaseDigestHex: hex.EncodeToString(created.Digest[:]), Body: body}); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("foreign SaveOperator112Draft = %v, want ErrNotFound", err)
	}
	// Neither copying the draft nor listing its versions is a way around
	// that: both are ErrNotFound for a foreign instructor too.
	if _, err := contentService.CreateOperator112Scenario(ctx, other.ID, content.ScenarioCreateInput{Title: "Копия черновика", Difficulty: 1, CopyFromVersionID: &created.VersionID}); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("foreign copy of draft = %v, want ErrNotFound", err)
	}
	if _, err := contentService.ScenarioVersions(ctx, other.ID, created.ID); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("foreign ScenarioVersions of draft = %v, want ErrNotFound", err)
	}
	if ownCopy, err := contentService.CreateOperator112Scenario(ctx, author.ID, content.ScenarioCreateInput{Title: "Своя копия", Difficulty: 1, CopyFromVersionID: &created.VersionID}); err != nil || ownCopy.CreatedBy != author.ID {
		t.Fatalf("own copy of draft = %+v, %v; want a new draft by author", ownCopy, err)
	}

	// Preview gate (review 2026-09-26, item 6): the stored version is
	// validated server-side — clean passes, a version with an error issue
	// is BlockingIssuesError, and someone else's is ErrNotFound.
	if err := contentService.CheckPreviewable(ctx, author.ID, created.ID, created.VersionID); err != nil {
		t.Fatalf("CheckPreviewable(clean) = %v, want nil", err)
	}
	if err := contentService.CheckPreviewable(ctx, other.ID, created.ID, created.VersionID); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("foreign CheckPreviewable = %v, want ErrNotFound", err)
	}
	broken := unmarshalScenarioBody(t, freeTextChatScenarioJSON)
	broken.Intake112.Reference.ExpectedTypes = nil
	brokenScenario, err := contentService.CreateOperator112Scenario(ctx, author.ID, content.ScenarioCreateInput{Title: "Кейс с ошибкой", Difficulty: 1, Body: &broken})
	if err != nil {
		t.Fatalf("create broken: %v", err)
	}
	var blocking *content.BlockingIssuesError
	if err := contentService.CheckPreviewable(ctx, author.ID, brokenScenario.ID, brokenScenario.VersionID); !errors.As(err, &blocking) {
		t.Fatalf("CheckPreviewable(broken) = %v, want BlockingIssuesError", err)
	}
	if err := contentService.CheckPreviewable(ctx, author.ID, created.ID, brokenScenario.VersionID); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("CheckPreviewable(version of another scenario) = %v, want ErrNotFound", err)
	}

	// A stale base_digest is rejected without creating a version.
	if _, err := contentService.SaveOperator112Draft(ctx, author.ID, created.ID, content.ScenarioEditInput{BaseDigestHex: "00", Body: body}); !errors.Is(err, content.ErrStaleDraft) {
		t.Fatalf("stale save = %v, want ErrStaleDraft", err)
	}

	newTitle := "Мой кейс, версия 2"
	saved, err := contentService.SaveOperator112Draft(ctx, author.ID, created.ID, content.ScenarioEditInput{
		BaseDigestHex: hex.EncodeToString(created.Digest[:]), Title: &newTitle, Body: body,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Version != 2 || saved.Title != newTitle {
		t.Fatalf("saved = %+v, want version=2 title=%q", saved, newTitle)
	}
	var v1Status string
	if err := pool.QueryRow(ctx, `SELECT status FROM scenario_versions WHERE id=$1`, created.VersionID).Scan(&v1Status); err != nil {
		t.Fatal(err)
	}
	if v1Status != "superseded" {
		t.Fatalf("v1 status = %q, want superseded (every save supersedes the prior version)", v1Status)
	}

	// Approve rejects a stale digest, then succeeds with the current one.
	if _, err := contentService.ApproveOperator112Scenario(ctx, author.ID, created.ID, saved.VersionID, "00"); !errors.Is(err, content.ErrStaleDraft) {
		t.Fatalf("stale approve = %v, want ErrStaleDraft", err)
	}
	approved, err := contentService.ApproveOperator112Scenario(ctx, author.ID, created.ID, saved.VersionID, hex.EncodeToString(saved.Digest[:]))
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != "approved" {
		t.Fatalf("approved.Status = %q, want approved", approved.Status)
	}

	// Any instructor may copy an approved scenario into a brand new one
	// under their own authorship — never edit the original directly.
	copied, err := contentService.CreateOperator112Scenario(ctx, other.ID, content.ScenarioCreateInput{
		Title: "Копия чужого кейса", Difficulty: 1, CopyFromVersionID: &approved.VersionID,
	})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if copied.ID == created.ID {
		t.Fatalf("copy reused the original scenario id")
	}
	if copied.CreatedBy != other.ID || copied.Status != "draft" {
		t.Fatalf("copied = %+v, want created_by=other status=draft", copied)
	}

	// Once published, a foreign instructor lists only published versions —
	// never the superseded v1 draft the author saved over.
	foreignVersions, err := contentService.ScenarioVersions(ctx, other.ID, created.ID)
	if err != nil {
		t.Fatalf("foreign ScenarioVersions: %v", err)
	}
	if len(foreignVersions) != 1 || foreignVersions[0].ID != approved.VersionID {
		t.Fatalf("foreign ScenarioVersions = %+v, want only the approved version", foreignVersions)
	}
	if _, err := contentService.CreateOperator112Scenario(ctx, other.ID, content.ScenarioCreateInput{Title: "Копия старого черновика", Difficulty: 1, CopyFromVersionID: &created.VersionID}); !errors.Is(err, content.ErrNotFound) {
		t.Fatalf("foreign copy of superseded draft = %v, want ErrNotFound", err)
	}
}

// TestScenarioListSurfacesOwnDraftsOnlyToTheirAuthor is a regression the
// 112-7 editor exposed in GET /scenarios (ScenarioCatalogueRoute, "Мои
// черновики" — see design decision reusing the existing status=draft
// filter, ADR-027): ListScenarios' own JOIN to scenario_versions used to
// require an *approved* version, so a scenario nobody has approved yet
// could never appear in this list at all, for anyone, including its own
// author browsing status=draft. RequestingUserID fixes exactly that case
// without changing visibility for anyone else.
func TestScenarioListSurfacesOwnDraftsOnlyToTheirAuthor(t *testing.T) {
	ctx := context.Background()
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	pool := openTestPool(t, ctx, databaseURL)
	author := insertInstructor(t, ctx, pool, "list-author-"+uuid.NewString())
	other := insertInstructor(t, ctx, pool, "list-other-"+uuid.NewString())
	contentService := content.NewService(contentpg.NewStore(pool), mustValidator(t))

	body := unmarshalScenarioBody(t, freeTextChatScenarioJSON)
	created, err := contentService.CreateOperator112Scenario(ctx, author.ID, content.ScenarioCreateInput{Title: "Список: черновик", Difficulty: 1, Body: &body})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	containsID := func(items []content.ScenarioSummary, id uuid.UUID) bool {
		for _, item := range items {
			if item.ID == id {
				return true
			}
		}
		return false
	}
	baseFilter := content.ScenarioFilter{Page: 1, PageSize: 50}

	approvedFilter := baseFilter
	approvedFilter.Status = "approved"
	approvedFilter.RequestingUserID = author.ID
	items, _, err := contentService.ListScenarios(ctx, approvedFilter)
	if err != nil {
		t.Fatal(err)
	}
	if containsID(items, created.ID) {
		t.Fatalf("status=approved listed an unapproved scenario")
	}

	draftAsAuthor := baseFilter
	draftAsAuthor.Status = "draft"
	draftAsAuthor.RequestingUserID = author.ID
	items, _, err = contentService.ListScenarios(ctx, draftAsAuthor)
	if err != nil {
		t.Fatal(err)
	}
	if !containsID(items, created.ID) {
		t.Fatalf("status=draft as its own author did not list the scenario: %+v", items)
	}

	draftAsOther := baseFilter
	draftAsOther.Status = "draft"
	draftAsOther.RequestingUserID = other.ID
	items, _, err = contentService.ListScenarios(ctx, draftAsOther)
	if err != nil {
		t.Fatal(err)
	}
	if containsID(items, created.ID) {
		t.Fatalf("status=draft as a different instructor leaked another author's draft")
	}

	draftNoRequester := baseFilter
	draftNoRequester.Status = "draft"
	items, _, err = contentService.ListScenarios(ctx, draftNoRequester)
	if err != nil {
		t.Fatal(err)
	}
	if containsID(items, created.ID) {
		t.Fatalf("status=draft with no requesting user leaked the draft")
	}
}

// unmarshalScenarioBody extracts {"body": ...} from a scenario-file JSON
// literal (the same shape ImportScenarios reads) into a content.Body —
// operator112_caller_chat_test.go's freeTextChatScenarioJSON is reused
// here verbatim rather than duplicating a minimal full_case/free_text
// fixture: it is already proven to pass structural Validate.
func unmarshalScenarioBody(t *testing.T, scenarioFileJSON string) content.Body {
	t.Helper()
	var file struct {
		Body content.Body `json:"body"`
	}
	if err := json.Unmarshal([]byte(scenarioFileJSON), &file); err != nil {
		t.Fatal(err)
	}
	return file.Body
}

// insertOwnedOperator112Version inserts sourceFile's own body (read
// straight off disk, the same technique
// operator112PipelineFixture.insertLegacyIncomingCallVersion already
// uses for a different scenario) as a new approved scenario owned by
// ownerID — StartPreview requires the acting instructor to be the
// scenario's own author, which none of the file-imported seed scenarios
// are (they belong to the import's admin identity).
func insertOwnedOperator112Version(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ownerID uuid.UUID, sourceFile, sourceKey, title string) (scenarioID, versionID uuid.UUID) {
	t.Helper()
	raw, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Body content.Body `json:"body"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	bodyJSON, err := json.Marshal(file.Body)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bodyJSON)
	scenarioID, versionID = uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO scenarios (id, source_key, title, difficulty, origin, status, created_by)
		VALUES ($1, $2, $3, 1, 'manual', 'approved', $4)`, scenarioID, sourceKey, title, ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO scenario_versions (id, scenario_id, version, status, body, digest, difficulty, exercise_type, created_by, approved_by, approved_at)
		VALUES ($1, $2, 1, 'approved', $3, $4, 1, 'operator112_intake', $5, $5, now())`, versionID, scenarioID, bodyJSON, digest[:], ownerID); err != nil {
		t.Fatal(err)
	}
	return scenarioID, versionID
}

// TestOperator112PreviewRunEndToEndExcludedFromStatistics is c6's own
// preview-mechanics test: StartPreview builds a real, workable one-off
// lesson/run/item with no workstation for its author, the author closes
// it exactly like a trainee closes their own item, the close still
// enqueues and produces a real rubric-v2 auto-assessment (112-7's own
// promise: "предпросмотр — не менее прослеживаем, просто не влияет на
// итог") — but the lesson never appears in the instructor's own lesson
// list, the item never appears in lesson_report_rows (and everything
// built on it: LessonReport/CSV/PDF, ResultsFor, ProgressFor), and an
// expert revision on it never advances trainee_assessment_state's
// version (the basis a future recommendation would read).
func TestOperator112PreviewRunEndToEndExcludedFromStatistics(t *testing.T) {
	ctx := context.Background()
	f := newOperator112PipelineFixture(t, ctx, "112-7 предпросмотр")
	taskStore := mustTaskEnqueuer(f.pool)
	assessmentService := newIntake112AssessmentServiceForTest(f.pool, taskStore)

	scenarioID, versionID := insertOwnedOperator112Version(t, ctx, f.pool, f.instructor.UserID,
		"../../seed/scenarios/pilot-112-full-gas-road-traffic-fire-01.json", "editor-preview-source", "Предпросмотр автора")

	lessonID, itemID, err := f.trainingService.StartPreview(ctx, f.instructor.UserID, scenarioID, versionID)
	if err != nil {
		t.Fatalf("StartPreview: %v", err)
	}

	var mode string
	if err := f.pool.QueryRow(ctx, `SELECT mode FROM lessons WHERE id=$1`, lessonID).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "preview" {
		t.Fatalf("lesson.mode = %q, want preview", mode)
	}
	var workstationID *uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT r.workstation_id FROM runs r JOIN items i ON i.run_id=r.id WHERE i.id=$1`, itemID).Scan(&workstationID); err != nil {
		t.Fatal(err)
	}
	if workstationID != nil {
		t.Fatalf("preview run.workstation_id = %v, want NULL", *workstationID)
	}

	// The instructor drives their own preview item exactly like a
	// trainee would drive theirs (ItemForTrainee/Execute do not branch on
	// role — only on run.UserID and workstationMatches).
	operator := auth.Principal{UserID: f.instructor.UserID, Role: auth.RoleInstructor}
	s := &commandSender{t: t, ctx: ctx, trainingService: f.trainingService, operator: operator, itemID: itemID}
	s.send(training.CommandOpen, map[string]any{})
	s.send(training.CommandAnswerIncoming, map[string]any{})
	s.send(training.CommandAskIntakeQuestion, map[string]string{"question_id": "ask_address"})
	s.send(training.CommandAskIntakeQuestion, map[string]string{"question_id": "clarify_house"})
	s.send(training.CommandAskIntakeQuestion, map[string]string{"question_id": "ask_victims"})
	s.send(training.CommandAddIncidentType, map[string]string{"type_id": "gas_explosion_road_traffic_fire"})
	item, _, _, err := f.trainingService.ItemForTrainee(ctx, operator, itemID)
	if err != nil {
		t.Fatal(err)
	}
	s.send(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard})
	item, _, _, err = f.trainingService.ItemForTrainee(ctx, operator, itemID)
	if err != nil {
		t.Fatal(err)
	}
	selected := make([]string, 0, len(item.IntakeState.SuggestedServices))
	for _, sv := range item.IntakeState.SuggestedServices {
		selected = append(selected, sv.ServiceCode)
	}
	s.send(training.CommandNotifyServices, map[string]any{"services": selected, "reason": ""})
	s.send(training.CommandEndIncoming, map[string]any{})
	s.send(training.CommandCompleteIntake, map[string]any{})

	// Close still produces a real auto-assessment (112-7's own promise:
	// the author sees a genuine rubric-v2 result, not a stub).
	status, found := evaluateTaskStatus(t, ctx, f.pool, itemID)
	if !found || status != "waiting" {
		t.Fatalf("evaluate task status = %q, found=%v, want waiting", status, found)
	}
	mustRunCoordinatorTick(t, ctx, f.pool, assessmentService)
	if handleErr, claimed := claimAndHandle(t, ctx, f.pool, taskStore, assessmentService, "test-worker-preview-1"); !claimed || handleErr != nil {
		t.Fatalf("Handle: claimed=%v err=%v", claimed, handleErr)
	}
	rows := readAssessments(t, ctx, f.pool, itemID)
	if len(rows) != 1 || rows[0].Kind != "auto" || rows[0].Status != "ready" {
		t.Fatalf("assessments for preview item = %+v, want exactly one ready auto", rows)
	}

	// Never in the instructor's own lesson list (though their real
	// training lesson from the fixture still is).
	lessons, err := f.trainingService.ListLessons(ctx, f.instructor, nil)
	if err != nil {
		t.Fatal(err)
	}
	sawPreview, sawReal := false, false
	for _, l := range lessons {
		if l.ID == lessonID {
			sawPreview = true
		}
		if l.ID == f.lesson.ID {
			sawReal = true
		}
	}
	if sawPreview {
		t.Fatalf("ListLessons included the preview lesson")
	}
	if !sawReal {
		t.Fatalf("ListLessons dropped the fixture's own real lesson too")
	}

	// Never in lesson_report_rows — the view lesson_report_rows/CSV/PDF/
	// ResultsFor/ProgressFor all read from, because runs.workstation_id
	// is NULL for a preview run and the view's own JOIN to workstations
	// is an inner join (migrations/00017, ADR-027's own "уточнение по
	// факту реализации").
	var reportRows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM lesson_report_rows WHERE item_id=$1`, itemID).Scan(&reportRows); err != nil {
		t.Fatal(err)
	}
	if reportRows != 0 {
		t.Fatalf("lesson_report_rows for preview item = %d rows, want 0", reportRows)
	}

	// An expert revision on a preview item never advances
	// trainee_assessment_state — CreateExpertRevision itself takes the
	// lock (creating the row at version=0 if absent), so the meaningful
	// check is that BumpTraineeStateVersion never runs afterward.
	auto := rows[0]
	before := traineeStateVersion(t, ctx, f.pool, f.instructor.UserID, content.ExerciseTypeOperator112Intake)
	if _, err := assessmentService.CreateExpertRevision(ctx, itemID, f.instructor.UserID, assessment.RevisionInput{
		BaseRevision: 1, Reason: "Автор проверяет свой же предпросмотр", Criteria: resolveAllCriteria(auto.Criteria),
	}, "preview-expert-review"); err != nil {
		t.Fatalf("CreateExpertRevision: %v", err)
	}
	after := traineeStateVersion(t, ctx, f.pool, f.instructor.UserID, content.ExerciseTypeOperator112Intake)
	if after != before {
		t.Fatalf("trainee_assessment_state.version moved %d -> %d for a preview item's expert revision, want unchanged", before, after)
	}
}

// traineeStateVersion reads trainee_assessment_state.version, treating a
// missing row as version 0 (LockTraineeState's own upsert has not run
// yet for this (user_id, exercise_type) pair).
func traineeStateVersion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, exerciseType content.ExerciseType) int {
	t.Helper()
	var version int
	err := pool.QueryRow(ctx, `SELECT version FROM trainee_assessment_state WHERE user_id=$1 AND exercise_type=$2`, userID, exerciseType).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return version
}
