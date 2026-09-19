package content

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// secretSentinel appears in every field of a full Body that must never
// reach a CardPreview — reference, hints, generation, events[].expects/
// text, field_corrections, pilot_goal, contact phrases, and an
// unknown nested address field a future schema change might add.
const secretSentinel = "SECRET-DO-NOT-LEAK-3f9a"

func fullSecretBody() Body {
	return Body{
		Schema:        "emsim/scenario/v1",
		TargetService: "dds_district",
		Card: Card{
			Number:              "881412",
			RegisteredAtOffsetS: -60,
			Applicant:           Applicant{Name: "Иванов", Phone: "+79161234567", Status: "witness"},
			Address:             Address{District: "d", Street: "s", House: "1", Text: "d, s, 1"},
			Incident: Incident{
				TypeCode: "14080106", TypeName: "n",
				Features:    map[string]any{"объект": "Дерево"},
				Description: "открытое описание, не эталон",
				Victims:     0,
			},
			NotificationList: []NotificationEntry{{Service: "dds_district", Status: ReactionAdded, Mine: true}},
		},
		Contacts: []Contact{{
			Key: "c1", Label: "l", Number: "100", Voice: "male_calm",
			Phrases: ContactPhrases{Greeting: secretSentinel, Ack: secretSentinel},
		}},
		Events: []Event{{
			Key: "e1", AtS: 1, Since: "accepted", Delivery: "notice", From: "c1",
			Text:  secretSentinel,
			Spawn: &EventSpawn{Kind: "duplicate", Variation: secretSentinel},
			Expects: &EventExpects{
				Status: ReactionAccepted, WithinS: 30, Action: secretSentinel,
			},
		}},
		Reference: Reference{
			PrimaryDecision: PrimaryDecision{
				Status: ReactionAccepted, ReasonTags: []string{secretSentinel},
				CommentMustMention: []string{secretSentinel},
			},
			ExpectedChain: []Reaction{ReactionAccepted},
			Call:          Call{Required: true, To: "c1", MustMention: []string{secretSentinel}},
			FieldCorrections: []FieldCorrection{
				{Path: "/card/address/okrug", ExpectedValue: secretSentinel, BeforeStatus: ReactionAccepted},
			},
			PilotGoal: secretSentinel,
			GuideRefs: []string{secretSentinel},
			Notes:     secretSentinel,
			Scoring:   &Scoring{Note: secretSentinel},
		},
		Hints: []Hint{{Step: secretSentinel, Text: secretSentinel, GuideRef: secretSentinel}},
		Generation: &Generation{
			Model: secretSentinel, PromptVersion: secretSentinel, ClassifierCode: secretSentinel,
		},
		Difficulty:   1,
		ExerciseType: ExerciseTypeDDSProcessing,
	}
}

func TestProjectCardDoesNotLeakClosedFields(t *testing.T) {
	body := fullSecretBody()
	preview := ProjectCard(body.Card)
	contacts := ProjectContacts(body.Contacts)

	b, err := json.Marshal(struct {
		Card     CardPreview
		Contacts []ContactPreview
	}{preview, contacts})
	if err != nil {
		t.Fatalf("marshal preview: %v", err)
	}
	if strings.Contains(string(b), secretSentinel) {
		t.Fatalf("CardPreview/ContactPreview leaks a closed field:\n%s", b)
	}
}

func TestProjectCardGoldenShape(t *testing.T) {
	card := Card{
		Number:              "881412",
		RegisteredAtOffsetS: -60,
		Applicant:           Applicant{Name: "Иванов", Phone: "+79161234567", Status: "witness"},
		Address:             Address{District: "Чертаново Южное", Street: "Чертановская улица", House: "58", Okrug: "ЮАР"},
		Incident: Incident{
			TypeCode: "14080106", TypeName: "Дерево упало во дворе",
			Features: map[string]any{"объект": "Дерево"}, Description: "учебное описание", Victims: 0,
		},
		NotificationList: []NotificationEntry{
			{Service: "dds_district", Status: ReactionAdded, Mine: true},
			{Service: "pilot_yuao_prefecture", Status: ReactionAdded},
		},
	}
	got := ProjectCard(card)
	want := CardPreview{
		Number:              "881412",
		RegisteredAtOffsetS: -60,
		Applicant:           ApplicantPreview{Name: "Иванов", Phone: "+79161234567", Status: "witness"},
		Address:             Address{District: "Чертаново Южное", Street: "Чертановская улица", House: "58", Okrug: "ЮАР"},
		Incident: IncidentPreview{
			TypeCode: "14080106", TypeName: "Дерево упало во дворе",
			Features: map[string]any{"объект": "Дерево"}, Description: "учебное описание", Victims: 0,
		},
		NotificationList: []NotificationPreview{
			{Service: "dds_district", Status: ReactionAdded, Mine: true},
			{Service: "pilot_yuao_prefecture", Status: ReactionAdded},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProjectCard =\n%+v\nwant\n%+v", got, want)
	}
}

func TestProjectContactsDropsPhrases(t *testing.T) {
	contacts := []Contact{{
		Key: "c1", Label: "Бригада", Number: "201", Voice: "male_calm",
		Phrases: ContactPhrases{Greeting: secretSentinel, Ack: secretSentinel},
	}}
	got := ProjectContacts(contacts)
	want := []ContactPreview{{Key: "c1", Label: "Бригада", Number: "201", Voice: "male_calm"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ProjectContacts = %+v, want %+v", got, want)
	}
}

// allowedPreviewLeafTypes are the only field kinds CardPreview/
// ApplicantPreview/IncidentPreview/NotificationPreview/ContactPreview may
// use. map[string]any is allowed only for IncidentPreview.Features
// (checked separately) — everywhere else it would be an opaque hole a
// future closed field could hide in without this test noticing.
func TestCardPreviewTypesHaveNoOpaqueFields(t *testing.T) {
	checkNoOpaqueFields(t, reflect.TypeOf(CardPreview{}), "")
}

func checkNoOpaqueFields(t *testing.T, typ reflect.Type, path string) {
	t.Helper()
	switch typ.Kind() {
	case reflect.Slice, reflect.Ptr:
		checkNoOpaqueFields(t, typ.Elem(), path+"[]")
	case reflect.String, reflect.Int, reflect.Bool:
		return
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			checkNoOpaqueFields(t, f.Type, path+"."+f.Name)
		}
	case reflect.Map:
		if path == ".Incident.Features" {
			return // the one deliberately open, allowlisted-by-content field
		}
		t.Fatalf("field %s is a map (%s) — CardPreview must only carry explicit allowlisted fields", path, typ)
	default:
		t.Fatalf("field %s has unexpected kind %s (%s) — add it to this test's allowlist deliberately", path, typ.Kind(), typ)
	}
}
