//go:build integration

package integration_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"

	pgstore "emsim/internal/platform/postgres"
)

func testWAV(sample byte) []byte {
	wav := make([]byte, 46)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], 38)
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], 1)
	binary.LittleEndian.PutUint32(wav[24:28], 16000)
	binary.LittleEndian.PutUint32(wav[28:32], 32000)
	binary.LittleEndian.PutUint16(wav[32:34], 2)
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:44], 2)
	wav[44] = sample
	return wav
}

func uploadRecording(t *testing.T, f trainingE2EFixture, client *http.Client, itemID, callID string, data []byte) (*http.Response, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="report.wav"`)
	h.Set("Content-Type", "audio/wav")
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(f.ctx, http.MethodPut, f.baseURL+"/api/v1/items/"+itemID+"/calls/"+callID+"/recording", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if response.StatusCode != http.StatusNoContent {
		_ = json.NewDecoder(response.Body).Decode(&envelope)
	}
	return response, envelope.Error.Code
}

func downloadBytes(t *testing.T, f trainingE2EFixture, client *http.Client, path string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, f.baseURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func TestTrainingPhoneCallRecordingAndEvidenceEndToEnd(t *testing.T) {
	f := setupTrainingE2E(t)
	admin := newCookieClient(t)
	loginAdmin(t, f, admin)
	if response := jsonRequest(t, f.ctx, admin, f.baseURL, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"login": "e2e-phone-instructor", "password": "correct-horse-battery-staple", "full_name": "Инструктор", "role": "instructor",
	}, nil); response.StatusCode != http.StatusCreated {
		t.Fatalf("create instructor status=%d", response.StatusCode)
	}
	instructor := newCookieClient(t)
	if response := jsonRequest(t, f.ctx, instructor, f.baseURL, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"login": "e2e-phone-instructor", "password": "correct-horse-battery-staple",
	}, nil); response.StatusCode != http.StatusOK {
		t.Fatalf("login instructor status=%d", response.StatusCode)
	}
	versionID := approvedVersionID(t, f, instructor, "pilot-phone-01")
	provisionWorkstations(t, f, admin, map[string]any{"number": 51, "label": "phone-a"}, map[string]any{"number": 52, "label": "phone-b"})
	traineeA := createTrainee(t, f, admin, "e2e-phone-a", 51)
	traineeB := createTrainee(t, f, admin, "e2e-phone-b", 52)
	runLesson(t, f, instructor, "Phone A", "training", 51, currentUserID(t, f, traineeA), versionID)
	runLesson(t, f, instructor, "Phone B", "training", 52, currentUserID(t, f, traineeB), versionID)
	itemA, itemB := currentItemID(t, f, traineeA), currentItemID(t, f, traineeB)
	for _, tc := range []struct {
		client *http.Client
		item   string
	}{{traineeA, itemA}, {traineeB, itemB}} {
		if response, receipt := sendCommand(t, f, tc.client, tc.item, randomCommandID(), 0, "open", map[string]any{}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
			t.Fatalf("open status=%d receipt=%+v", response.StatusCode, receipt)
		}
	}

	startCommandID := randomCommandID()
	response, startA := sendCommand(t, f, traineeA, itemA, startCommandID, 1, "call_start", map[string]any{"contact": "crew_leader"})
	if response.StatusCode != http.StatusOK || startA.CallID == "" {
		t.Fatalf("start A status=%d receipt=%+v", response.StatusCode, startA)
	}
	response, replay := sendCommand(t, f, traineeA, itemA, startCommandID, 1, "call_start", map[string]any{"contact": "crew_leader"})
	if response.StatusCode != http.StatusOK || !replay.Replayed || replay.CallID != startA.CallID {
		t.Fatalf("start replay status=%d receipt=%+v", response.StatusCode, replay)
	}
	response, startB := sendCommand(t, f, traineeB, itemB, randomCommandID(), 1, "call_start", map[string]any{"contact": "crew_leader"})
	if response.StatusCode != http.StatusOK || startB.CallID == "" || startB.CallID == startA.CallID {
		t.Fatalf("independent start B status=%d receipt=%+v", response.StatusCode, startB)
	}

	phrasePath := "/api/v1/items/" + itemA + "/contacts/crew_leader/phrases/greeting"
	if phraseResponse, phrase := downloadBytes(t, f, traineeA, phrasePath); phraseResponse.StatusCode != http.StatusOK || phraseResponse.Header.Get("Content-Type") != "audio/wav" || !bytes.HasPrefix(phrase, []byte("RIFF")) {
		t.Fatalf("greeting status=%d type=%q bytes=%d", phraseResponse.StatusCode, phraseResponse.Header.Get("Content-Type"), len(phrase))
	}

	recordingA := testWAV(1)
	digestA := sha256.Sum256(recordingA)
	response, receipt := sendCommand(t, f, traineeA, itemA, randomCommandID(), 2, "call_end", map[string]any{
		"call_id": startA.CallID, "accepted_by": "Иванов", "summary": "Чертановская 62, дерево упало, участок ограждён",
		"recording": map[string]any{"sha256": hex.EncodeToString(digestA[:]), "size": len(recordingA), "mime": "audio/wav"},
	})
	if response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("end A status=%d receipt=%+v", response.StatusCode, receipt)
	}
	ackPath := "/api/v1/items/" + itemA + "/contacts/crew_leader/phrases/ack"
	if ackResponse, ack := downloadBytes(t, f, traineeA, ackPath); ackResponse.StatusCode != http.StatusOK || !bytes.HasPrefix(ack, []byte("RIFF")) {
		t.Fatalf("ack status=%d bytes=%d", ackResponse.StatusCode, len(ack))
	}
	if upload, code := uploadRecording(t, f, traineeA, itemA, startA.CallID, testWAV(2)); upload.StatusCode != http.StatusConflict || code != "recording_conflict" {
		t.Fatalf("conflicting upload status=%d code=%q", upload.StatusCode, code)
	}
	if upload, code := uploadRecording(t, f, traineeA, itemA, startA.CallID, recordingA); upload.StatusCode != http.StatusNoContent {
		t.Fatalf("upload status=%d code=%q", upload.StatusCode, code)
	}

	// A second call with recording=null is the denied/unavailable microphone path.
	response, nullStart := sendCommand(t, f, traineeA, itemA, randomCommandID(), 3, "call_start", map[string]any{"contact": "crew_leader"})
	if response.StatusCode != http.StatusOK || nullStart.CallID == "" {
		t.Fatalf("null start status=%d receipt=%+v", response.StatusCode, nullStart)
	}
	if response, receipt = sendCommand(t, f, traineeA, itemA, randomCommandID(), 4, "call_end", map[string]any{"call_id": nullStart.CallID, "accepted_by": "Петров", "summary": "Повторный доклад", "recording": nil}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("null end status=%d receipt=%+v", response.StatusCode, receipt)
	}
	if response, receipt = sendCommand(t, f, traineeA, itemA, randomCommandID(), 5, "set_status", map[string]any{"status": "accepted"}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("accept A status=%d receipt=%+v", response.StatusCode, receipt)
	}
	if response, receipt = sendCommand(t, f, traineeA, itemA, randomCommandID(), 6, "close", map[string]any{}); response.StatusCode != http.StatusOK || receipt.ItemState != "closed" {
		t.Fatalf("close A status=%d receipt=%+v", response.StatusCode, receipt)
	}
	if upload, code := uploadRecording(t, f, traineeA, itemA, startA.CallID, recordingA); upload.StatusCode != http.StatusNoContent {
		t.Fatalf("ready replay status=%d code=%q", upload.StatusCode, code)
	}
	recordingPath := "/api/v1/items/" + itemA + "/calls/" + startA.CallID + "/recording"
	if gotResponse, data := downloadBytes(t, f, instructor, recordingPath); gotResponse.StatusCode != http.StatusOK || !bytes.Equal(data, recordingA) {
		t.Fatalf("instructor download status=%d bytes=%d", gotResponse.StatusCode, len(data))
	}
	if gotResponse, _ := downloadBytes(t, f, traineeA, recordingPath); gotResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("trainee download status=%d", gotResponse.StatusCode)
	}

	// B loses the in-memory bytes, closes, and reaches the immutable deadline.
	recordingB := testWAV(3)
	digestB := sha256.Sum256(recordingB)
	if response, receipt = sendCommand(t, f, traineeB, itemB, randomCommandID(), 2, "call_end", map[string]any{
		"call_id": startB.CallID, "accepted_by": "Сидоров", "summary": "Доклад без байтов",
		"recording": map[string]any{"sha256": hex.EncodeToString(digestB[:]), "size": len(recordingB), "mime": "audio/wav"},
	}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("end B status=%d receipt=%+v", response.StatusCode, receipt)
	}
	if response, receipt = sendCommand(t, f, traineeB, itemB, randomCommandID(), 3, "set_status", map[string]any{"status": "accepted"}); response.StatusCode != http.StatusOK || receipt.Outcome != "applied" {
		t.Fatalf("accept B status=%d receipt=%+v", response.StatusCode, receipt)
	}
	if response, receipt = sendCommand(t, f, traineeB, itemB, randomCommandID(), 4, "close", map[string]any{}); response.StatusCode != http.StatusOK || receipt.ItemState != "closed" {
		t.Fatalf("close B status=%d receipt=%+v", response.StatusCode, receipt)
	}
	pool, err := pgstore.Open(f.ctx, f.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(f.ctx, `UPDATE calls SET recording_upload_deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1`, startB.CallID); err != nil {
		t.Fatal(err)
	}
	if upload, code := uploadRecording(t, f, traineeB, itemB, startB.CallID, recordingB); upload.StatusCode != http.StatusConflict || code != "recording_deadline_passed" {
		t.Fatalf("late upload status=%d code=%q", upload.StatusCode, code)
	}
	var itemView struct {
		Calls []struct {
			RecordingState string `json:"recording_state"`
		} `json:"calls"`
	}
	if itemResponse := jsonRequest(t, f.ctx, traineeB, f.baseURL, http.MethodGet, "/api/v1/items/"+itemB, nil, &itemView); itemResponse.StatusCode != http.StatusOK || len(itemView.Calls) != 1 || itemView.Calls[0].RecordingState != "expired" {
		t.Fatalf("expired state status=%d item=%+v", itemResponse.StatusCode, itemView)
	}

	var evidence []byte
	if err = pool.QueryRow(f.ctx, `SELECT body FROM evidence WHERE item_id=$1`, itemA).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Calls   []json.RawMessage `json:"calls"`
		Derived struct {
			CallCount int `json:"call_count"`
		} `json:"derived"`
	}
	if err = json.Unmarshal(evidence, &decoded); err != nil || len(decoded.Calls) != 2 || decoded.Derived.CallCount != 2 {
		t.Fatalf("evidence calls=%d count=%d err=%v", len(decoded.Calls), decoded.Derived.CallCount, err)
	}
}
