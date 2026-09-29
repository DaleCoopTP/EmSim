package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"emsim/internal/auth"
	"emsim/internal/training"

	"github.com/google/uuid"
)

func dictationRequest(contentType string, body []byte) *http.Request {
	r := trainingRequest("POST", "/api/v1/items/"+uuid.New().String()+"/dictation", body, true)
	r.Header.Set("Content-Type", contentType)
	return r
}

func TestDictationEndpoint(t *testing.T) {
	wav := bytes.Repeat([]byte{1}, 100)
	principal := trainingPrincipal(auth.RoleTrainee)

	svc := trainingFixture()
	svc.dictInfo = training.DictationInfo{Available: true, MaxSeconds: 30}
	svc.dictResult = training.DictationResult{Text: "горит дом", Model: "stub", DurationMS: 12}
	response := httptest.NewRecorder()
	trainingMux(svc, principal).ServeHTTP(response, dictationRequest("audio/wav", wav))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var got struct {
		Text       string `json:"text"`
		Model      string `json:"model"`
		DurationMS int64  `json:"duration_ms"`
	}
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil || got.Text != "горит дом" || got.Model != "stub" || got.DurationMS != 12 {
		t.Fatalf("body = %+v, err = %v", got, err)
	}
	if !bytes.Equal(svc.dictated, wav) {
		t.Fatal("the service did not receive the uploaded audio")
	}

	for name, tc := range map[string]struct {
		info        training.DictationInfo
		err         error
		contentType string
		status      int
		code        string
	}{
		"engine off":  {training.DictationInfo{}, nil, "audio/wav", 503, "dictation_unavailable"},
		"wrong type":  {training.DictationInfo{Available: true, MaxSeconds: 30}, nil, "audio/webm", 415, "unsupported_media_type"},
		"busy":        {training.DictationInfo{Available: true, MaxSeconds: 30}, training.ErrDictationBusy, "audio/wav", 429, "dictation_busy"},
		"engine down": {training.DictationInfo{Available: true, MaxSeconds: 30}, training.ErrDictationUnavailable, "audio/wav", 503, "dictation_unavailable"},
		"not allowed": {training.DictationInfo{Available: true, MaxSeconds: 30}, training.ErrDictationNotAllowed, "audio/wav", 422, "transition_not_allowed"},
		"not owner":   {training.DictationInfo{Available: true, MaxSeconds: 30}, training.ErrNotFound, "audio/wav", 404, "not_found"},
	} {
		svc := trainingFixture()
		svc.dictInfo, svc.dictErr = tc.info, tc.err
		response := httptest.NewRecorder()
		trainingMux(svc, principal).ServeHTTP(response, dictationRequest(tc.contentType, wav))
		t.Run(name, func(t *testing.T) { requireError(t, response, tc.status, tc.code) })
	}
}

func TestDictationEndpointRejectsOversizedAndUnauthenticated(t *testing.T) {
	svc := trainingFixture()
	svc.dictInfo = training.DictationInfo{Available: true, MaxSeconds: 5}
	response := httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleTrainee)).ServeHTTP(response, dictationRequest("audio/wav", make([]byte, training.MaxDictationBytes(5)+1)))
	requireError(t, response, http.StatusRequestEntityTooLarge, "payload_too_large")

	response = httptest.NewRecorder()
	unauthenticated := trainingRequest("POST", "/api/v1/items/"+uuid.New().String()+"/dictation", []byte("x"), false)
	unauthenticated.Header.Set("Content-Type", "audio/wav")
	trainingMux(svc, trainingPrincipal(auth.RoleTrainee)).ServeHTTP(response, unauthenticated)
	requireError(t, response, http.StatusUnauthorized, "unauthorized")

	response = httptest.NewRecorder()
	trainingMux(svc, trainingPrincipal(auth.RoleAdmin)).ServeHTTP(response, dictationRequest("audio/wav", []byte("x")))
	requireError(t, response, http.StatusForbidden, "forbidden")
}

func TestItemProjectionCarriesDictationHint(t *testing.T) {
	get := func(svc *fakeTraining) map[string]any {
		t.Helper()
		response := httptest.NewRecorder()
		trainingMux(svc, trainingPrincipal(auth.RoleTrainee)).ServeHTTP(response, trainingRequest("GET", "/api/v1/items/"+svc.item.ID.String(), nil, true))
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	chat := func() *fakeTraining {
		svc := trainingFixture()
		svc.item.IntakeCard = &training.IntakeCard{}
		svc.item.IntakeState = &training.IntakeState{Mode: "full_case", CallStatus: "connected"}
		return svc
	}

	on := chat()
	on.dictInfo = training.DictationInfo{Available: true, MaxSeconds: 30}
	hint, _ := get(on)["dictation"].(map[string]any)
	if hint["available"] != true || hint["max_seconds"] != float64(30) {
		t.Fatalf("dictation hint = %v", hint)
	}
	if _, present := get(chat())["dictation"]; present {
		t.Fatal("no hint while the engine is off")
	}
	dds := trainingFixture()
	dds.dictInfo = training.DictationInfo{Available: true, MaxSeconds: 30}
	if _, present := get(dds)["dictation"]; present {
		t.Fatal("a DDS item must never carry a dictation hint")
	}
}
