package http

import (
	"encoding/json"
	"strings"
	"testing"

	"emsim/internal/content"
	"emsim/internal/training"
)

func TestTraineeIntakeProjectionOnlyRevealsActiveProfiles(t *testing.T) {
	full := &content.IntakeCatalog{Version: 1,
		Types: []content.IntakeIncidentType{{ID: "gas_explosion", Name: "Взрыв газа", ProfileIDs: []string{"104"}},
			{ID: "road_traffic_fire", Name: "ДТП с пламенем", ProfileIDs: []string{"101"}}},
		Profiles: []content.IntakeProfile{{ID: "104", Version: 1, Fields: []content.IntakeProfileField{{ID: "smell_location"}}},
			{ID: "101", Version: 1, Fields: []content.IntakeProfileField{{ID: "medical_help"}}}},
		ServiceRules: []content.IntakeServiceRule{{ID: "gas", ProfileID: "104", ServiceCode: "pilot_gas_104"},
			{ID: "fire", ProfileID: "101", ServiceCode: "pilot_fire_101"},
			{ID: "medical", ProfileID: "101", FieldID: "medical_help", Equals: "Да", ServiceCode: "pilot_ambulance"}},
	}
	item := training.Item{IntakeCard: &training.IntakeCard{Profiles: map[string]training.IntakeProfile{}},
		IntakeState: &training.IntakeState{Mode: "card_only", Catalog: full,
			InactiveProfiles: map[string]training.IntakeProfile{"101": {DefinitionID: "101"}}}}
	services := availableServiceCodesForState(item.IntakeState)
	state := traineeIntakeState(item)
	if len(state.Catalog.Profiles) != 0 || len(state.Catalog.ServiceRules) != 0 || state.InactiveProfiles != nil || len(services) != 3 {
		t.Fatalf("initial projection leaked definitions: %+v, %v", state, services)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "medical_help") || strings.Contains(string(encoded), "smell_location") || strings.Contains(string(encoded), "pilot_ambulance") || strings.Contains(string(encoded), "profile_ids\":[\"104\"]") {
		t.Fatalf("initial projection exposed future fields or rules: %s", encoded)
	}
	item.IntakeCard.Profiles["104"] = training.IntakeProfile{DefinitionID: "104"}
	state = traineeIntakeState(item)
	if len(state.Catalog.Profiles) != 1 || state.Catalog.Profiles[0].ID != "104" || len(full.Profiles) != 2 || len(full.ServiceRules) != 3 {
		t.Fatalf("active projection or stored catalog changed: %+v, %+v", state.Catalog, full)
	}
}
