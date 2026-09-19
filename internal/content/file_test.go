package content

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const minimalFileJSON = `{
  "schema": "emsim/scenario-file/v1",
  "key": "pilot-tree-01",
  "version": 1,
  "title": "Дерево во дворе — 01",
  "origin": "manual",
  "body": {
    "schema": "emsim/scenario/v1",
    "target_service": "dds_district",
    "card": {
      "number": "881412",
      "registered_at_offset_s": -60,
      "applicant": {"name": "Иванов", "phone": "+79161234567", "status": "witness"},
      "address": {"district": "Чертаново Южное", "street": "Чертановская улица", "house": "58", "okrug": "ЮАР"},
      "incident": {
        "type_code": "14080106",
        "type_name": "Дерево упало во дворе",
        "features": {"объект": "Дерево"},
        "description": "Учебное описание происшествия для пилотного кейса номер один.",
        "victims": 0
      },
      "notification_list": [{"service": "dds_district", "status": "added", "mine": true}]
    },
    "contacts": [],
    "events": [],
    "reference": {
      "primary_decision": {"status": "accepted"},
      "expected_chain": [],
      "call": {"required": false},
      "field_corrections": [{"path": "/card/address/okrug", "expected_value": "ЮАО", "before_status": "accepted"}],
      "pilot_goal": "accept_card"
    },
    "difficulty": 1,
    "exercise_type": "dds_processing"
  }
}`

func TestDecodeFileTyped(t *testing.T) {
	raw, file, err := DecodeFile(strings.NewReader(minimalFileJSON))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if file.Key != "pilot-tree-01" || file.Version != 1 || file.Origin != "manual" {
		t.Fatalf("unexpected file: %+v", file)
	}
	if file.Body.Card.Address.Okrug != "ЮАР" {
		t.Fatalf("card.address.okrug = %q, want ЮАР (the pilot's deliberate error)", file.Body.Card.Address.Okrug)
	}
	if len(file.Body.Reference.FieldCorrections) != 1 || file.Body.Reference.FieldCorrections[0].ExpectedValue != "ЮАО" {
		t.Fatalf("field_corrections not decoded: %+v", file.Body.Reference.FieldCorrections)
	}
	if _, err := bodyRaw(raw); err != nil {
		t.Fatalf("bodyRaw(raw): %v", err)
	}
}

func TestDecodeFileEventVoiceDefault(t *testing.T) {
	const withEvent = `{"key":"e1","at_s":10,"since":"accepted","delivery":"notice"}`
	var e Event
	if err := json.Unmarshal([]byte(withEvent), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !e.Voice {
		t.Fatalf("event.voice should default to true when omitted")
	}

	const withVoiceFalse = `{"key":"e1","at_s":10,"since":"accepted","delivery":"notice","voice":false}`
	var e2 Event
	if err := json.Unmarshal([]byte(withVoiceFalse), &e2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e2.Voice {
		t.Fatalf("event.voice=false must not be overridden by the default")
	}
}

func TestDecodeFileRejectsDuplicateKeys(t *testing.T) {
	const dup = `{"schema":"emsim/scenario-file/v1","key":"a","key":"b","version":1,"title":"t","origin":"manual","body":{}}`
	_, _, err := DecodeFile(strings.NewReader(dup))
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("DecodeFile(dup key) error = %v, want ErrDuplicateKey", err)
	}
}

func TestDecodeFileRejectsNestedDuplicateKeys(t *testing.T) {
	const dup = `{"schema":"emsim/scenario-file/v1","key":"a","version":1,"title":"t","origin":"manual","body":{"card":{"number":"1","number":"2"}}}`
	_, _, err := DecodeFile(strings.NewReader(dup))
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("DecodeFile(nested dup key) error = %v, want ErrDuplicateKey", err)
	}
}

func TestDecodeFileRejectsTrailingContent(t *testing.T) {
	const trailing = `{"schema":"emsim/scenario-file/v1","key":"a","version":1,"title":"t","origin":"manual","body":{}}garbage`
	_, _, err := DecodeFile(strings.NewReader(trailing))
	if err == nil {
		t.Fatalf("DecodeFile(trailing content) should fail")
	}
}

func TestBodyDigestUsesBodyOnly(t *testing.T) {
	raw, _, err := DecodeFile(strings.NewReader(minimalFileJSON))
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	digest, err := BodyDigest(raw)
	if err != nil {
		t.Fatalf("BodyDigest: %v", err)
	}
	rawB := raw.(map[string]any)
	bodyOnly, err := bodyRaw(rawB)
	if err != nil {
		t.Fatalf("bodyRaw: %v", err)
	}
	want := Digest(bodyOnly)
	if digest != want {
		t.Fatalf("BodyDigest mismatch")
	}
	// Changing the wrapper (key) without touching body must not change
	// the digest — it is sha256(body) only.
	rawB["key"] = "different-key"
	digest2, err := BodyDigest(rawB)
	if err != nil {
		t.Fatalf("BodyDigest: %v", err)
	}
	if digest2 != digest {
		t.Fatalf("BodyDigest changed when only the file wrapper changed")
	}
}
