package operator112

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"

	"github.com/google/uuid"
)

func pilotCatalog(t *testing.T) content.IntakeCatalog {
	t.Helper()
	f, err := os.Open("../../../seed/intake-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := content.DecodeIntakeCatalog(f)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestProfileCasesAndServiceSuggestions(t *testing.T) {
	catalog := pilotCatalog(t)
	for _, tc := range []struct {
		typeID   string
		profiles int
		services []string
	}{
		{"gas_explosion", 1, []string{"pilot_gas_104"}},
		{"road_traffic_fire", 1, []string{"pilot_fire_101"}},
		{"gas_explosion_road_traffic_fire", 2, []string{"pilot_gas_104", "pilot_fire_101"}},
	} {
		t.Run(tc.typeID, func(t *testing.T) {
			card := training.UnansweredIntakeCard("112-test", "", "", "")
			card.Profiles = map[string]training.IntakeProfile{}
			state := training.IntakeState{Mode: "card_only", CallStatus: "not_applicable", Catalog: &catalog, Transcript: []training.IntakeLine{}}
			item := training.Item{ID: uuid.New(), State: training.ItemOffered, IntakeCard: &card, IntakeState: &state}
			run := func(typ training.CommandType, p any) training.Decision {
				t.Helper()
				data, err := json.Marshal(p)
				if err != nil {
					t.Fatal(err)
				}
				d, err := New().Decide(item, training.Command{Type: typ, Payload: data}, time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
				if err != nil {
					t.Fatal(err)
				}
				if d.Accepted {
					item.State, item.IntakeCard, item.IntakeState = d.State, d.IntakeCard, d.IntakeState
				}
				return d
			}
			if d := run(training.CommandCompleteProfileCase, map[string]any{}); d.Accepted {
				t.Fatal("completed without type")
			}
			if d := run(training.CommandOpen, map[string]any{}); !d.Accepted {
				t.Fatal("open")
			}
			if d := run(training.CommandAnswerIncoming, map[string]any{}); d.Accepted {
				t.Fatal("call action in card-only mode")
			}
			if len(item.IntakeCard.Profiles) != 0 {
				t.Fatal("profile appeared before type selection")
			}
			if d := run(training.CommandAddIncidentType, map[string]string{"type_id": tc.typeID}); !d.Accepted {
				t.Fatal("add")
			}
			if len(item.IntakeCard.Profiles) != tc.profiles {
				t.Fatalf("profiles: %d", len(item.IntakeCard.Profiles))
			}
			if d := run(training.CommandAddIncidentType, map[string]string{"type_id": tc.typeID}); d.Accepted {
				t.Fatal("duplicate type accepted")
			}
			for i, suggestion := range item.IntakeState.SuggestedServices {
				if i >= len(tc.services) || suggestion.ServiceCode != tc.services[i] {
					t.Fatalf("suggestions: %+v", item.IntakeState.SuggestedServices)
				}
			}
			if len(item.IntakeState.SuggestedServices) != len(tc.services) {
				t.Fatalf("suggestions: %+v", item.IntakeState.SuggestedServices)
			}
			if d := run(training.CommandReviewServices, map[string]any{"services": tc.services, "reason": ""}); d.Accepted {
				t.Fatal("review before save")
			}
			if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard}); !d.Accepted {
				t.Fatalf("save: %+v", d)
			}
			if d := run(training.CommandReviewServices, map[string]any{"services": tc.services, "reason": ""}); !d.Accepted {
				t.Fatal("review")
			}
			if d := run(training.CommandCompleteProfileCase, map[string]any{}); !d.Accepted || d.IntakeDispatch != nil || d.State != training.ItemClosed {
				t.Fatalf("complete: %+v", d)
			}
		})
	}
}

func TestProfileMedicalHelpAndRemovalRestore(t *testing.T) {
	catalog := pilotCatalog(t)
	card := training.UnansweredIntakeCard("112-test", "", "", "")
	card.Profiles = map[string]training.IntakeProfile{}
	state := training.IntakeState{Mode: "card_only", CallStatus: "not_applicable", Catalog: &catalog}
	item := training.Item{State: training.ItemOpened, IntakeCard: &card, IntakeState: &state}
	run := func(typ training.CommandType, p any) training.Decision {
		t.Helper()
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		d, err := New().Decide(item, training.Command{Type: typ, Payload: data}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if d.Accepted {
			item.State, item.IntakeCard, item.IntakeState = d.State, d.IntakeCard, d.IntakeState
		}
		return d
	}
	run(training.CommandAddIncidentType, map[string]string{"type_id": "road_traffic_fire"})
	for _, answer := range []training.IntakeProfileAnswer{{State: "unknown"}, {State: "known", Value: "Нет"}} {
		profile := item.IntakeCard.Profiles["101"]
		profile.Answers["medical_help"] = answer
		item.IntakeCard.Profiles["101"] = profile
		if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard}); !d.Accepted {
			t.Fatal("save unknown/no")
		}
		if len(item.IntakeState.SuggestedServices) != 1 {
			t.Fatalf("unexpected medical suggestion: %+v", item.IntakeState.SuggestedServices)
		}
	}
	profile := item.IntakeCard.Profiles["101"]
	profile.Answers["medical_help"] = training.IntakeProfileAnswer{State: "known", Value: "Да"}
	item.IntakeCard.Profiles["101"] = profile
	if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard}); !d.Accepted {
		t.Fatal("save yes")
	}
	if len(item.IntakeState.SuggestedServices) != 2 || item.IntakeState.SuggestedServices[1].ServiceCode != "pilot_ambulance" {
		t.Fatalf("medical suggestion: %+v", item.IntakeState.SuggestedServices)
	}
	if d := run(training.CommandReviewServices, map[string]any{"services": []string{"pilot_fire_101"}, "reason": ""}); d.Accepted {
		t.Fatal("adjustment without reason")
	}
	if d := run(training.CommandReviewServices, map[string]any{"services": []string{"pilot_fire_101"}, "reason": "Учебное решение"}); !d.Accepted || d.Effect == nil {
		t.Fatal("adjustment and its original suggestion must be recorded")
	}
	if d := run(training.CommandRemoveIncidentType, map[string]string{"type_id": "road_traffic_fire"}); !d.Accepted {
		t.Fatal("remove")
	}
	if len(item.IntakeCard.Profiles) != 0 || len(item.IntakeState.SuggestedServices) != 0 || item.IntakeState.ServiceReview != nil {
		t.Fatal("inactive profile affected active card")
	}
	if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard}); !d.Accepted {
		t.Fatal("save after removing the last type")
	}
	if d := run(training.CommandAddIncidentType, map[string]string{"type_id": "road_traffic_fire"}); !d.Accepted {
		t.Fatal("restore")
	}
	if item.IntakeCard.Profiles["101"].Answers["medical_help"].Value != "Да" {
		t.Fatal("answer not restored")
	}
}
