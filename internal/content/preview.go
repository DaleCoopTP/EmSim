package content

// CardPreview is the allowlist projection of Card that both an
// instructor's preview (GET /scenarios/{id}/preview, C4) and a trainee's
// runtime view (slice 3) are built from — openapi.yaml's CardPreview
// schema, field for field. Every field here is a plain value type
// (string/int/bool/[]T of the same), never map[string]any or
// json.RawMessage, and Reference/Hints/Generation/Event.Expects/
// FieldCorrection/PilotGoal have no field here at all: ProjectCard
// cannot leak what it never has room to copy (see preview_test.go's
// reflect-based check). Incident.Features is the one exception — it is
// itself allowlisted content (the classifier's опросная карта answers,
// shown to the trainee as-is), not instructor/scoring data.
//
// json tags match openapi.yaml's CardPreview/CardView property names:
// content/http keeps its own DTO for the instructor preview response
// (handlers.go's cardPreviewJSON) since that endpoint's field set is not
// identical, but internal/training marshals CardPreview directly (an
// item's card instance, and evidence.final_card, slice 3) and needs a
// stable, snake_case shape rather than Go's default capitalized field
// names — struct tags do not affect ProjectCard's struct-conversion from
// NotificationEntry above (Go ignores tags for convertibility).
type CardPreview struct {
	Number              string                `json:"number"`
	RegisteredAtOffsetS int                   `json:"registered_at_offset_s"`
	Applicant           ApplicantPreview      `json:"applicant"`
	Address             Address               `json:"address"`
	Incident            IncidentPreview       `json:"incident"`
	NotificationList    []NotificationPreview `json:"notification_list"`
	Phones              Phones                `json:"phones"`
	Channel             string                `json:"channel"`
	Contacts            []ContactPreview      `json:"contacts,omitempty"`
}

type ApplicantPreview struct {
	Name   string `json:"name"`
	Phone  string `json:"phone"`
	Status string `json:"status"`
}

type IncidentPreview struct {
	TypeCode    string         `json:"type_code"`
	TypeName    string         `json:"type_name"`
	Features    map[string]any `json:"features"`
	Description string         `json:"description"`
	Victims     int            `json:"victims"`
	Danger      string         `json:"danger"`
}

type NotificationPreview struct {
	Service string   `json:"service"`
	Status  Reaction `json:"status"`
	Mine    bool     `json:"mine"`
}

// ContactPreview is Contact without Phrases (openapi.yaml's Contact
// schema): greeting/ack text is voice-call content a trainee hears when
// they call, not something the catalogue or scoring reference exposes
// ahead of time.
type ContactPreview struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Number string `json:"number"`
	Voice  string `json:"voice"`
}

// ProjectCard builds a CardPreview from a scenario version's Card — the
// one place internal/content narrows a scenario's full content down to
// what a trainee (and, unmodified, an instructor's preview) may see.
func ProjectCard(card Card) CardPreview {
	notifications := make([]NotificationPreview, len(card.NotificationList))
	for i, n := range card.NotificationList {
		notifications[i] = NotificationPreview(n)
	}
	return CardPreview{
		Number:              card.Number,
		RegisteredAtOffsetS: card.RegisteredAtOffsetS,
		Applicant: ApplicantPreview{
			Name:   card.Applicant.Name,
			Phone:  card.Applicant.Phone,
			Status: card.Applicant.Status,
		},
		Address: card.Address,
		Incident: IncidentPreview{
			TypeCode:    card.Incident.TypeCode,
			TypeName:    card.Incident.TypeName,
			Features:    card.Incident.Features,
			Description: card.Incident.Description,
			Victims:     card.Incident.Victims,
			Danger:      card.Incident.Danger,
		},
		NotificationList: notifications,
		Phones:           card.Phones,
		Channel:          card.Channel,
	}
}

// ProjectContacts builds the CardPreview.Contacts allowlist from a
// scenario's Contacts — kept separate from ProjectCard because Card
// itself carries no contacts (scenario.schema.json has contacts as a
// sibling of card, not nested under it).
func ProjectContacts(contacts []Contact) []ContactPreview {
	out := make([]ContactPreview, len(contacts))
	for i, c := range contacts {
		out[i] = ContactPreview{Key: c.Key, Label: c.Label, Number: c.Number, Voice: c.Voice}
	}
	return out
}
