package training

import (
	"strings"
	"time"
)

// IntakeField keeps absence of an answer distinct from an explicit unknown
// answer. Negative is reserved for the presence question.
type IntakeField struct {
	State string `json:"state"`
	Value string `json:"value,omitempty"`
}

type IntakeAddress struct {
	City     IntakeField `json:"city"`
	Street   IntakeField `json:"street"`
	House    IntakeField `json:"house"`
	Building IntakeField `json:"building"`
	Flat     IntakeField `json:"flat"`
	Landmark IntakeField `json:"landmark"`
}

// IntakeCard is the operator's mutable draft. Metadata is copied from the
// approved call and never accepted from a command payload.
type IntakeCard struct {
	Number          string        `json:"number"`
	AON             string        `json:"aon"`
	CallLocalTime   string        `json:"call_local_time"`
	CallTimeZone    string        `json:"call_time_zone"`
	ApplicantName   IntakeField   `json:"applicant_name"`
	ApplicantStatus IntakeField   `json:"applicant_status"`
	Age             IntakeField   `json:"age"`
	Address         IntakeAddress `json:"address"`
	IncidentType    IntakeField   `json:"incident_type"`
	Complaint       IntakeField   `json:"complaint"`
	VictimsPresent  IntakeField   `json:"victims_present"`
	VictimsCount    IntakeField   `json:"victims_count"`
	ProvidedPhone   IntakeField   `json:"provided_phone"`
}

func UnansweredIntakeCard(number, aon, localTime, zone string) IntakeCard {
	u := IntakeField{State: "unanswered"}
	return IntakeCard{Number: number, AON: aon, CallLocalTime: localTime, CallTimeZone: zone,
		ApplicantName: u, ApplicantStatus: u, Age: u, IncidentType: u, Complaint: u,
		VictimsPresent: u, VictimsCount: u, ProvidedPhone: u,
		Address: IntakeAddress{City: u, Street: u, House: u, Building: u, Flat: u, Landmark: u}}
}

// ValidIntakeCard validates only the form's representation, not correctness
// against the scenario's private reference. Incomplete cards may be saved.
func ValidIntakeCard(card IntakeCard) bool {
	fields := []IntakeField{card.ApplicantName, card.ApplicantStatus, card.Age,
		card.Address.City, card.Address.Street, card.Address.House, card.Address.Building,
		card.Address.Flat, card.Address.Landmark, card.IncidentType, card.Complaint,
		card.VictimsPresent, card.VictimsCount, card.ProvidedPhone}
	for i, field := range fields {
		if len(field.Value) > 1000 || strings.TrimSpace(field.Value) != field.Value {
			return false
		}
		switch field.State {
		case "known":
			if field.Value == "" {
				return false
			}
		case "unanswered", "unknown":
			if field.Value != "" {
				return false
			}
		case "negative":
			if i != 11 || field.Value != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

type IntakeLine struct {
	Text     string    `json:"text"`
	ServerAt time.Time `json:"server_at"`
}

type IntakeState struct {
	CallStatus      string       `json:"call_status"` // ringing, connected, ended
	Transcript      []IntakeLine `json:"transcript"`
	HasSavedDraft   bool         `json:"has_saved_draft"`
	Dispatched      bool         `json:"dispatched"`
	SelectedService string       `json:"selected_service,omitempty"`
	AnsweredAt      *time.Time   `json:"answered_at,omitempty"`
	EndedAt         *time.Time   `json:"ended_at,omitempty"`
}

type IntakeDispatch struct {
	ItemID       string     `json:"item_id"`
	ActionID     string     `json:"action_id"`
	ServiceCode  string     `json:"service_code"`
	CardSnapshot IntakeCard `json:"card_snapshot"`
	SentAt       time.Time  `json:"sent_at"`
}
