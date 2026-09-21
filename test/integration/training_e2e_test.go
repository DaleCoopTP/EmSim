//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pgstore "emsim/internal/platform/postgres"

	"github.com/google/uuid"
)

// trainingE2EFixture builds one "emsim" binary, migrates a fresh
// database, bootstraps an admin, imports the shipped seed/ catalogue and
// starts one "emsim api" process — the shared setup both training e2e
// tests in this file need, following api_process_test.go's own
// per-test-process convention (TestAPIProcessContentCatalogAccess).
type trainingE2EFixture struct {
	ctx         context.Context
	databaseURL string
	baseURL     string
}

func setupTrainingE2E(t *testing.T) trainingE2EFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	databaseURL := openTestDatabase(t, ctx)
	if err := pgstore.Up(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(t.TempDir(), "emsim")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/emsim")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build emsim: %v\n%s", err, output)
	}

	const adminLogin, adminPassword = "e2e-training-admin", "correct-horse-battery-staple"
	runBootstrapAdminProcess(t, ctx, binary, databaseURL, adminLogin, adminPassword)
	blobRoot := runImportSeedProcess(t, ctx, binary, databaseURL, adminLogin, "../../seed")

	publicAddr, adminAddr := freeAddr(t), freeAddr(t)
	api := startAPIProcess(t, binary, databaseURL, publicAddr, adminAddr, blobRoot)
	t.Cleanup(func() { api.stop(t) })
	return trainingE2EFixture{ctx: ctx, databaseURL: databaseURL, baseURL: "http://" + publicAddr}
}

func newCookieClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 5 * time.Second}
}

func loginAdmin(t *testing.T, f trainingE2EFixture, client *http.Client) {
	t.Helper()
	if response := jsonRequest(t, f.ctx, client, f.baseURL, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"login": "e2e-training-admin", "password": "correct-horse-battery-staple"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("admin login status = %d", response.StatusCode)
	}
}

// approvedVersionID resolves scenarioSourceKey (a seed file's "key",
// e.g. "pilot-tree-01") to its approved version id, through the same two
// instructor-only catalogue endpoints the web UI's LessonDetail screen
// uses (GET /scenarios then GET /scenarios/{id}/versions).
func approvedVersionID(t *testing.T, f trainingE2EFixture, instructorClient *http.Client, scenarioSourceKey string) string {
	t.Helper()
	var scenarioList struct {
		Items []struct {
			ID        string  `json:"id"`
			SourceKey *string `json:"source_key"`
		} `json:"items"`
	}
	response := jsonRequest(t, f.ctx, instructorClient, f.baseURL, http.MethodGet, "/api/v1/scenarios?page_size=200", nil, &scenarioList)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios status = %d", response.StatusCode)
	}
	var scenarioID string
	for _, item := range scenarioList.Items {
		if item.SourceKey != nil && *item.SourceKey == scenarioSourceKey {
			scenarioID = item.ID
		}
	}
	if scenarioID == "" {
		t.Fatalf("%s not found in scenario list: %+v", scenarioSourceKey, scenarioList.Items)
	}

	var versions []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	response = jsonRequest(t, f.ctx, instructorClient, f.baseURL, http.MethodGet, "/api/v1/scenarios/"+scenarioID+"/versions", nil, &versions)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios/%s/versions status = %d", scenarioID, response.StatusCode)
	}
	for _, v := range versions {
		if v.Status == "approved" {
			return v.ID
		}
	}
	t.Fatalf("%s has no approved version: %+v", scenarioSourceKey, versions)
	return ""
}

type lessonResponse struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

type receiptResponse struct {
	CommandID string  `json:"command_id"`
	Outcome   string  `json:"outcome"`
	Seq       int     `json:"seq"`
	Reaction  string  `json:"reaction"`
	ItemState string  `json:"item_state"`
	Replayed  bool    `json:"replayed"`
	ErrorCode *string `json:"error_code"`
	CallID    string  `json:"call_id"`
}

// provisionWorkstations replaces the active workstation inventory in one
// request. The admin endpoint has replacement semantics, so provisioning
// workstations one-by-one would deactivate the ones from earlier calls.
func provisionWorkstations(t *testing.T, f trainingE2EFixture, adminClient *http.Client, workstations ...map[string]any) {
	t.Helper()
	response := jsonRequest(t, f.ctx, adminClient, f.baseURL, http.MethodPut, "/api/v1/admin/workstations", workstations, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("provision workstations status = %d", response.StatusCode)
	}
}

// createTrainee provisions one active trainee through the admin API and
// returns a session-authenticated client for that trainee. Its workstation
// must already exist in the replacement inventory installed above.
func createTrainee(t *testing.T, f trainingE2EFixture, adminClient *http.Client, login string, workstationNo int) *http.Client {
	t.Helper()
	response := jsonRequest(t, f.ctx, adminClient, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"login": login, "password": "correct-horse-battery-staple",
		"full_name": "Курсант " + login, "role": "trainee", "service_code": "dds_district",
	}, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create trainee %s status = %d", login, response.StatusCode)
	}
	client := newCookieClient(t)
	response = jsonRequest(t, f.ctx, client, f.baseURL, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"login": login, "password": "correct-horse-battery-staple", "workstation_no": workstationNo}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("trainee %s login status = %d", login, response.StatusCode)
	}
	return client
}

type meIDResponse struct {
	User struct {
		ID string `json:"id"`
	} `json:"user"`
}

// currentUserID reads GET /me through client's own session and returns
// the user id — used once per trainee right after createTrainee, since
// the admin API's create-user response is not itself session-scoped.
func currentUserID(t *testing.T, f trainingE2EFixture, client *http.Client) string {
	t.Helper()
	var me meIDResponse
	response := jsonRequest(t, f.ctx, client, f.baseURL, http.MethodGet, "/api/v1/me", nil, &me)
	if response.StatusCode != http.StatusOK || me.User.ID == "" {
		t.Fatalf("GET /me status = %d, body = %+v", response.StatusCode, me)
	}
	return me.User.ID
}

// runLesson creates a draft lesson, assigns versionID to traineeUserID on
// workstationNo, and starts it — the instructor side of every scenario
// below.
func runLesson(t *testing.T, f trainingE2EFixture, instructorClient *http.Client, title, mode string, workstationNo int, traineeUserID, versionID string) lessonResponse {
	t.Helper()
	var lesson lessonResponse
	response := jsonRequest(t, f.ctx, instructorClient, f.baseURL, http.MethodPost, "/api/v1/lessons", map[string]any{
		"exercise_type": "dds_processing", "title": title, "mode": mode, "level": "easy",
	}, &lesson)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create lesson %q status = %d", title, response.StatusCode)
	}
	response = jsonRequest(t, f.ctx, instructorClient, f.baseURL, http.MethodPut, "/api/v1/lessons/"+lesson.ID+"/assignments",
		[]map[string]any{{"workstation_no": workstationNo, "user_id": traineeUserID, "scenario_version_ids": []string{versionID}}}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("assign lesson %q status = %d", title, response.StatusCode)
	}
	response = jsonRequest(t, f.ctx, instructorClient, f.baseURL, http.MethodPost, "/api/v1/lessons/"+lesson.ID+"/start", nil, &lesson)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("start lesson %q status = %d", title, response.StatusCode)
	}
	return lesson
}

// currentItemID reads the trainee's one active item id via /my/run — the
// same lookup a fresh page load performs (RFC-001 §7.7's snapshot read).
func currentItemID(t *testing.T, f trainingE2EFixture, traineeClient *http.Client) string {
	t.Helper()
	var run struct {
		CurrentItemID *string `json:"current_item_id"`
		QueueLeft     int     `json:"queue_left"`
	}
	response := jsonRequest(t, f.ctx, traineeClient, f.baseURL, http.MethodGet, "/api/v1/my/run", nil, &run)
	// queue_left is "not yet offered" (assignment length - QueueCursor,
	// the same semantics Monitor.rows[].queue_left uses), not a count of
	// currently open items — a single-item assignment's one card is
	// already offered by the time /my/run is read here, so queue_left is
	// 0, not 1.
	if response.StatusCode != http.StatusOK || run.CurrentItemID == nil || run.QueueLeft != 0 {
		t.Fatalf("GET /my/run status = %d, body = %+v", response.StatusCode, run)
	}
	return *run.CurrentItemID
}

func sendCommand(t *testing.T, f trainingE2EFixture, client *http.Client, itemID, commandID string, expectedSeq int, cmdType string, payload map[string]any) (*http.Response, receiptResponse) {
	t.Helper()
	var receipt receiptResponse
	response := jsonRequest(t, f.ctx, client, f.baseURL, http.MethodPost, "/api/v1/items/"+itemID+"/actions", map[string]any{
		"command_id": commandID, "expected_seq": expectedSeq, "type": cmdType, "payload": payload,
	}, &receipt)
	return response, receipt
}

func randomCommandID() string {
	return uuid.New().String()
}

// TestAPIProcessTrainingPilotOneEndToEnd drives slice 3's whole vertical
// (slice-planning.md §4) purely over HTTP against a real "emsim api"
// process: an instructor assigns the SAME approved pilot-tree-01 version
// to two independent trainees (two independent runs of one version),
// each opens, accepts and closes their own card, a lost response is
// recovered via ADR-004 replay, the trainee's own item view never
// carries a reference, the instructor's does, and a closed run frees its
// user/workstation slot for a new lesson.
func TestAPIProcessTrainingPilotOneEndToEnd(t *testing.T) {
	f := setupTrainingE2E(t)
	adminClient := newCookieClient(t)
	loginAdmin(t, f, adminClient)

	var instructor struct {
		ID string `json:"id"`
	}
	if response := jsonRequest(t, f.ctx, adminClient, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"login": "e2e-training-instructor", "password": "correct-horse-battery-staple",
		"full_name": "Инструктор Занятий", "role": "instructor",
	}, &instructor); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor status = %d", response.StatusCode)
	}
	instructorClient := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructorClient, f.baseURL, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"login": "e2e-training-instructor", "password": "correct-horse-battery-staple"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("instructor login status = %d", response.StatusCode)
	}
	versionID := approvedVersionID(t, f, instructorClient, "pilot-tree-01")

	provisionWorkstations(t, f, adminClient,
		map[string]any{"number": 1, "label": "e2e-trainee-a"},
		map[string]any{"number": 2, "label": "e2e-trainee-b"},
	)
	traineeAClient := createTrainee(t, f, adminClient, "e2e-trainee-a", 1)
	traineeBClient := createTrainee(t, f, adminClient, "e2e-trainee-b", 2)
	traineeUserAID := currentUserID(t, f, traineeAClient)
	traineeUserBID := currentUserID(t, f, traineeBClient)

	runLesson(t, f, instructorClient, "Пилот 01 — курсант A", "training", 1, traineeUserAID, versionID)
	runLesson(t, f, instructorClient, "Пилот 01 — курсант B (intro)", "intro", 2, traineeUserBID, versionID)

	// -------------------------------------------------- trainee A: happy path + replay
	itemID := currentItemID(t, f, traineeAClient)

	// The trainee's own view never carries a reference, hints, or the
	// pilot_goal/field_corrections fields those imply — checked on the raw
	// JSON keys, not just the typed DTO, so a future field added to
	// itemJSON without a json tag audit cannot silently leak it.
	var rawItem map[string]json.RawMessage
	response := jsonRequest(t, f.ctx, traineeAClient, f.baseURL, http.MethodGet, "/api/v1/items/"+itemID, nil, &rawItem)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("trainee GET item status = %d", response.StatusCode)
	}
	for _, forbidden := range []string{"reference", "hints", "pilot_goal", "field_corrections"} {
		if _, present := rawItem[forbidden]; present {
			t.Fatalf("trainee item response leaked %q: %v", forbidden, rawItem)
		}
	}

	openCmd := randomCommandID()
	resp, receipt := sendCommand(t, f, traineeAClient, itemID, openCmd, 0, "open", map[string]any{})
	if resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" || receipt.Seq != 1 {
		t.Fatalf("open status=%d receipt=%+v", resp.StatusCode, receipt)
	}

	// ADR-004 replay: the client never saw the response above (simulated
	// by simply sending the identical body again) and must get back the
	// exact same outcome/seq with replayed=true, not a second effect.
	resp, replay := sendCommand(t, f, traineeAClient, itemID, openCmd, 0, "open", map[string]any{})
	if resp.StatusCode != http.StatusOK || !replay.Replayed || replay.Seq != 1 || replay.Outcome != "applied" {
		t.Fatalf("replayed open status=%d receipt=%+v", resp.StatusCode, replay)
	}

	acceptCmd := randomCommandID()
	resp, receipt = sendCommand(t, f, traineeAClient, itemID, acceptCmd, 1, "set_status", map[string]any{"status": "accepted"})
	if resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" || receipt.Reaction != "accepted" {
		t.Fatalf("accept status=%d receipt=%+v", resp.StatusCode, receipt)
	}

	closeCmd := randomCommandID()
	resp, receipt = sendCommand(t, f, traineeAClient, itemID, closeCmd, 2, "close", map[string]any{})
	if resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" || receipt.ItemState != "closed" {
		t.Fatalf("close status=%d receipt=%+v", resp.StatusCode, receipt)
	}

	var evidenceRow struct {
		CloseReason string
		FinalOkrug  string
	}
	pool, err := pgstore.Open(f.ctx, f.databaseURL)
	if err != nil {
		t.Fatalf("open pool for evidence check: %v", err)
	}
	defer pool.Close()
	var body []byte
	if err := pool.QueryRow(f.ctx, `SELECT body FROM evidence WHERE item_id = $1`, itemID).Scan(&body); err != nil {
		t.Fatalf("read evidence row: %v", err)
	}
	var decoded struct {
		CloseReason string `json:"close_reason"`
		FinalCard   struct {
			Address struct {
				Okrug string `json:"okrug"`
			} `json:"address"`
		} `json:"final_card"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode evidence body: %v", err)
	}
	evidenceRow.CloseReason, evidenceRow.FinalOkrug = decoded.CloseReason, decoded.FinalCard.Address.Okrug
	if evidenceRow.CloseReason != "pilot_completed" || evidenceRow.FinalOkrug != "ЮАО" {
		t.Fatalf("evidence = %+v, want close_reason=pilot_completed and okrug=ЮАО (already correct in pilot-tree-01)", evidenceRow)
	}

	// The instructor's own view of the same item DOES carry the reference
	// (this is the one place it is ever allowed on the wire).
	response = jsonRequest(t, f.ctx, instructorClient, f.baseURL, http.MethodGet, "/api/v1/items/"+itemID, nil, &rawItem)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("instructor GET item status = %d", response.StatusCode)
	}
	if _, present := rawItem["reference"]; !present || !strings.Contains(string(rawItem["reference"]), "accept_card") {
		t.Fatalf("instructor item missing reference/pilot_goal: %v", rawItem)
	}

	// A closed run's user/workstation slot is free again (slice-planning.md
	// §4 DoD) — a brand new lesson for the same trainee+workstation must
	// start without conflict.
	restarted := runLesson(t, f, instructorClient, "Пилот 01 — курсант A, повтор", "training", 1, traineeUserAID, versionID)
	if restarted.State != "running" {
		t.Fatalf("restarted lesson state = %q, want running", restarted.State)
	}

	// -------------------------------------------------- trainee B: intro mode, no side effects
	itemBID := currentItemID(t, f, traineeBClient)
	if resp, receipt := sendCommand(t, f, traineeBClient, itemBID, randomCommandID(), 0, "open", map[string]any{}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("intro open status=%d receipt=%+v", resp.StatusCode, receipt)
	}
	if resp, receipt := sendCommand(t, f, traineeBClient, itemBID, randomCommandID(), 1, "set_status", map[string]any{"status": "accepted"}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("intro accept status=%d receipt=%+v", resp.StatusCode, receipt)
	}
	if resp, receipt := sendCommand(t, f, traineeBClient, itemBID, randomCommandID(), 2, "close", map[string]any{}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("intro close status=%d receipt=%+v", resp.StatusCode, receipt)
	}
	// Slice 6: trainee A's training close enqueued exactly one
	// assessment.evaluate (waiting, for itemID); trainee B's intro close
	// must not add a second (slice-planning.md §9: intro never gets one).
	var taskCount int
	if err := pool.QueryRow(f.ctx, `SELECT count(*) FROM tasks`).Scan(&taskCount); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("tasks table has %d rows after training close + intro close, want 1 (only trainee A's assessment.evaluate)", taskCount)
	}
	var evaluateStatus, evaluateScopeID string
	if err := pool.QueryRow(f.ctx, `SELECT status, scope_id::text FROM tasks WHERE kind = 'assessment.evaluate'`).Scan(&evaluateStatus, &evaluateScopeID); err != nil {
		t.Fatalf("read assessment.evaluate task: %v", err)
	}
	if evaluateStatus != "waiting" || evaluateScopeID != itemID {
		t.Fatalf("assessment.evaluate task = status=%q scope_id=%q, want waiting/%s", evaluateStatus, evaluateScopeID, itemID)
	}
	var traineeBLevel string
	if err := pool.QueryRow(f.ctx, `SELECT level FROM users WHERE id = $1`, traineeUserBID).Scan(&traineeBLevel); err != nil {
		t.Fatalf("read trainee B level: %v", err)
	}
	if traineeBLevel != "easy" {
		t.Fatalf("trainee B level = %q, want unchanged easy", traineeBLevel)
	}
}

// TestAPIProcessTrainingFieldCorrectionPilot covers pilot-tree-02, whose
// card starts with an intentionally wrong okrug (ADR-017): one trainee
// corrects it via set_card_field before accepting, another accepts
// without correcting — both closes are allowed (pilot_completed), and
// only the corrected run's evidence carries the fixed value.
func TestAPIProcessTrainingFieldCorrectionPilot(t *testing.T) {
	f := setupTrainingE2E(t)
	adminClient := newCookieClient(t)
	loginAdmin(t, f, adminClient)

	if response := jsonRequest(t, f.ctx, adminClient, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"login": "e2e-fc-instructor", "password": "correct-horse-battery-staple",
		"full_name": "Инструктор Коррекции", "role": "instructor",
	}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor status = %d", response.StatusCode)
	}
	instructorClient := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructorClient, f.baseURL, http.MethodPost, "/api/v1/auth/login",
		map[string]any{"login": "e2e-fc-instructor", "password": "correct-horse-battery-staple"}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("instructor login status = %d", response.StatusCode)
	}
	versionID := approvedVersionID(t, f, instructorClient, "pilot-tree-02")

	pool, err := pgstore.Open(f.ctx, f.databaseURL)
	if err != nil {
		t.Fatalf("open pool for evidence check: %v", err)
	}
	defer pool.Close()
	provisionWorkstations(t, f, adminClient,
		map[string]any{"number": 1, "label": "e2e-fc-corrected"},
		map[string]any{"number": 2, "label": "e2e-fc-uncorrected"},
	)

	finalOkrug := func(itemID string) string {
		var body []byte
		if err := pool.QueryRow(f.ctx, `SELECT body FROM evidence WHERE item_id = $1`, itemID).Scan(&body); err != nil {
			t.Fatalf("read evidence row: %v", err)
		}
		var decoded struct {
			CloseReason string `json:"close_reason"`
			FinalCard   struct {
				Address struct {
					Okrug string `json:"okrug"`
				} `json:"address"`
			} `json:"final_card"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode evidence body: %v", err)
		}
		if decoded.CloseReason != "pilot_completed" {
			t.Fatalf("close_reason = %q, want pilot_completed", decoded.CloseReason)
		}
		return decoded.FinalCard.Address.Okrug
	}

	// -------------------------------------------------- corrects the field
	correctedClient := createTrainee(t, f, adminClient, "e2e-fc-corrected", 1)
	var correctedUser struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if response := jsonRequest(t, f.ctx, correctedClient, f.baseURL, http.MethodGet, "/api/v1/me", nil, &correctedUser); response.StatusCode != http.StatusOK {
		t.Fatalf("corrected trainee /me status = %d", response.StatusCode)
	}
	runLesson(t, f, instructorClient, "Пилот 02 — с исправлением", "training", 1, correctedUser.User.ID, versionID)
	correctedItemID := currentItemID(t, f, correctedClient)

	if resp, receipt := sendCommand(t, f, correctedClient, correctedItemID, randomCommandID(), 0, "open", map[string]any{}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("open status=%d receipt=%+v", resp.StatusCode, receipt)
	}
	resp, receipt := sendCommand(t, f, correctedClient, correctedItemID, randomCommandID(), 1, "set_card_field", map[string]any{"path": "/card/address/okrug", "value": "ЮАО"})
	if resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("set_card_field status=%d receipt=%+v", resp.StatusCode, receipt)
	}
	var correctedItem struct {
		Card struct {
			Address struct {
				Okrug string `json:"okrug"`
			} `json:"address"`
		} `json:"card"`
	}
	if response := jsonRequest(t, f.ctx, correctedClient, f.baseURL, http.MethodGet, "/api/v1/items/"+correctedItemID, nil, &correctedItem); response.StatusCode != http.StatusOK || correctedItem.Card.Address.Okrug != "ЮАО" {
		t.Fatalf("corrected card okrug = %+v, want ЮАО", correctedItem)
	}
	if resp, receipt := sendCommand(t, f, correctedClient, correctedItemID, randomCommandID(), 2, "set_status", map[string]any{"status": "accepted"}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("accept status=%d receipt=%+v", resp.StatusCode, receipt)
	}
	if resp, receipt := sendCommand(t, f, correctedClient, correctedItemID, randomCommandID(), 3, "close", map[string]any{}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("close status=%d receipt=%+v", resp.StatusCode, receipt)
	}
	if got := finalOkrug(correctedItemID); got != "ЮАО" {
		t.Fatalf("corrected run final okrug = %q, want ЮАО", got)
	}

	// -------------------------------------------------- accepts uncorrected
	uncorrectedClient := createTrainee(t, f, adminClient, "e2e-fc-uncorrected", 2)
	var uncorrectedUser struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if response := jsonRequest(t, f.ctx, uncorrectedClient, f.baseURL, http.MethodGet, "/api/v1/me", nil, &uncorrectedUser); response.StatusCode != http.StatusOK {
		t.Fatalf("uncorrected trainee /me status = %d", response.StatusCode)
	}
	runLesson(t, f, instructorClient, "Пилот 02 — без исправления", "training", 2, uncorrectedUser.User.ID, versionID)
	uncorrectedItemID := currentItemID(t, f, uncorrectedClient)

	if resp, receipt := sendCommand(t, f, uncorrectedClient, uncorrectedItemID, randomCommandID(), 0, "open", map[string]any{}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("open status=%d receipt=%+v", resp.StatusCode, receipt)
	}
	if resp, receipt := sendCommand(t, f, uncorrectedClient, uncorrectedItemID, randomCommandID(), 1, "set_status", map[string]any{"status": "accepted"}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("accept status=%d receipt=%+v", resp.StatusCode, receipt)
	}
	// close is allowed from accepted without ever correcting the field —
	// ADR-017's pilot exception does not enforce the reference's own
	// field_corrections, it only permits close_reason=pilot_completed.
	if resp, receipt := sendCommand(t, f, uncorrectedClient, uncorrectedItemID, randomCommandID(), 2, "close", map[string]any{}); resp.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("close without correction status=%d receipt=%+v", resp.StatusCode, receipt)
	}
	if got := finalOkrug(uncorrectedItemID); got != "ЮАР" {
		t.Fatalf("uncorrected run final okrug = %q, want the original ЮАР (uncorrected)", got)
	}
}
