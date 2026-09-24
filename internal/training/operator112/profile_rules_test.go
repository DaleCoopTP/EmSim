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

// TestProfileNotifyServicesFinale is ADR-023's card_only route: save,
// notify_services, complete_intake — replacing review_service_selection
// and complete_profile_case for items created with Finale="notify".
func TestProfileNotifyServicesFinale(t *testing.T) {
	catalog := pilotCatalog(t)

	t.Run("happy path", func(t *testing.T) {
		card := training.UnansweredIntakeCard("112-test", "", "", "")
		card.Profiles = map[string]training.IntakeProfile{}
		state := training.IntakeState{Mode: "card_only", Finale: "notify", CallStatus: "not_applicable", Catalog: &catalog, Transcript: []training.IntakeLine{}}
		item := training.Item{ID: uuid.New(), State: training.ItemOffered, IntakeCard: &card, IntakeState: &state}
		run := func(typ training.CommandType, p any) training.Decision {
			t.Helper()
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			d, err := New().Decide(item, training.Command{Type: typ, Payload: data}, time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if d.Accepted {
				item.State, item.IntakeCard, item.IntakeState = d.State, d.IntakeCard, d.IntakeState
			}
			return d
		}
		if d := run(training.CommandNotifyServices, map[string]any{"services": []string{"pilot_gas_104"}, "reason": ""}); d.Accepted {
			t.Fatal("notify without a saved draft")
		}
		run(training.CommandOpen, map[string]any{})
		if d := run(training.CommandAddIncidentType, map[string]string{"type_id": "gas_explosion"}); !d.Accepted {
			t.Fatalf("add: %+v", d)
		}
		if d := run(training.CommandReviewServices, map[string]any{"services": []string{"pilot_gas_104"}, "reason": ""}); d.Accepted {
			t.Fatal("legacy review_service_selection must reject once finale is notify")
		}
		if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard}); !d.Accepted {
			t.Fatalf("save: %+v", d)
		}
		if d := run(training.CommandNotifyServices, map[string]any{"services": []string{}, "reason": ""}); d.Accepted {
			t.Fatal("notify with an empty service list")
		}
		if d := run(training.CommandNotifyServices, map[string]any{"services": []string{"pilot_gas_104", "unknown_service"}, "reason": ""}); d.Accepted {
			t.Fatal("notify with a service outside the catalog's rules")
		}
		if d := run(training.CommandNotifyServices, map[string]any{"services": []string{"pilot_gas_104", "pilot_fire_101"}, "reason": ""}); d.Accepted {
			t.Fatal("notify adding a service beyond the suggestion without a reason")
		}
		d := run(training.CommandNotifyServices, map[string]any{"services": []string{"pilot_gas_104", "pilot_fire_101"}, "reason": "Учебное решение"})
		if !d.Accepted || d.IntakeNotification == nil || len(d.IntakeNotification.Services) != 2 {
			t.Fatalf("notify: %+v", d)
		}
		if !d.IntakeNotification.Services[0].Suggested || d.IntakeNotification.Services[1].Suggested {
			t.Fatalf("suggested flags: %+v", d.IntakeNotification.Services)
		}
		if !item.IntakeState.Notified {
			t.Fatal("state not marked notified")
		}
		if d := run(training.CommandNotifyServices, map[string]any{"services": []string{"pilot_gas_104"}, "reason": ""}); d.Accepted {
			t.Fatal("second notify must be rejected")
		}
		if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard}); d.Accepted {
			t.Fatal("draft edit after notify must be rejected")
		}
		if d := run(training.CommandAddIncidentType, map[string]string{"type_id": "road_traffic_fire"}); d.Accepted {
			t.Fatal("incident type edit after notify must be rejected")
		}
		if d := run(training.CommandCompleteProfileCase, map[string]any{}); d.Accepted {
			t.Fatal("legacy complete_profile_case must reject once finale is notify")
		}
		if d := run(training.CommandCompleteIntake, map[string]any{}); !d.Accepted || d.State != training.ItemClosed {
			t.Fatalf("complete_intake: %+v", d)
		}
	})
}

// TestFullCaseFlow is ADR-023/slice-112-4-plan.md's combined mode: the
// 112-2 caller dialogue and the 112-3 profile cards in one item, finished
// through the same notify_services/complete_intake pair card_only uses.
func TestFullCaseFlow(t *testing.T) {
	catalog := pilotCatalog(t)
	dialogue := &content.Intake112Dialogue{
		Initial: content.Intake112Utterance{ID: "initial", Text: "Пахнет газом", Reveals: []string{}},
		Questions: []content.Intake112Question{
			{ID: "where", Text: "Где именно?", TopicID: "address", Answer: content.Intake112Utterance{ID: "where_answer", Text: "В квартире", Reveals: []string{}}},
		},
	}
	card := training.UnansweredIntakeCard("112-full", "+79161313131", "02:03", "Europe/Moscow")
	card.Profiles = map[string]training.IntakeProfile{}
	state := training.IntakeState{Mode: "full_case", Finale: "notify", CallStatus: "ringing", Catalog: &catalog, Transcript: []training.IntakeLine{}}
	item := training.Item{ID: uuid.New(), State: training.ItemOffered, IntakeCard: &card, IntakeState: &state, IntakeDialogue: dialogue}
	run := func(typ training.CommandType, p any) training.Decision {
		t.Helper()
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		d, err := New().Decide(item, training.Command{Type: typ, Payload: data}, time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		if d.Accepted {
			item.State, item.IntakeCard, item.IntakeState = d.State, d.IntakeCard, d.IntakeState
		}
		return d
	}
	if d := run(training.CommandDispatchIntake, map[string]string{"service_code": "pilot_gas_104"}); d.Accepted {
		t.Fatal("dispatch_intake must reject for full_case")
	}
	if d := run(training.CommandOpen, map[string]any{}); !d.Accepted {
		t.Fatalf("open: %+v", d)
	}
	if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard}); d.Accepted {
		t.Fatal("save before answering the call")
	}
	if d := run(training.CommandAnswerIncoming, map[string]any{}); !d.Accepted || len(d.IntakeState.Transcript) != 1 {
		t.Fatalf("answer: %+v", d)
	}
	if d := run(training.CommandAskIntakeQuestion, map[string]string{"question_id": "where"}); !d.Accepted || len(d.IntakeState.Transcript) != 3 {
		t.Fatalf("ask: %+v", d)
	}
	if d := run(training.CommandAddIncidentType, map[string]string{"type_id": "gas_explosion"}); !d.Accepted || len(d.IntakeCard.Profiles) != 1 {
		t.Fatalf("add type: %+v", d)
	}
	if d := run(training.CommandSaveIntakeDraft, map[string]any{"draft": item.IntakeCard}); !d.Accepted {
		t.Fatalf("save: %+v", d)
	}
	if d := run(training.CommandNotifyServices, map[string]any{"services": []string{"pilot_gas_104"}, "reason": ""}); !d.Accepted || d.IntakeNotification == nil {
		t.Fatalf("notify: %+v", d)
	}
	if d := run(training.CommandCompleteIntake, map[string]any{}); d.Accepted {
		t.Fatal("complete_intake must reject before the call ends")
	}
	if d := run(training.CommandEndIncoming, map[string]any{}); !d.Accepted {
		t.Fatalf("end: %+v", d)
	}
	if d := run(training.CommandCompleteIntake, map[string]any{}); !d.Accepted || d.State != training.ItemClosed {
		t.Fatalf("complete: %+v", d)
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
