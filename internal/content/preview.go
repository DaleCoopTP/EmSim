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
type CardPreview struct {
	Number              string
	RegisteredAtOffsetS int
	Applicant           ApplicantPreview
	Address             Address
	Incident            IncidentPreview
	NotificationList    []NotificationPreview
	Phones              Phones
	Channel             string
	Contacts            []ContactPreview
}

type ApplicantPreview struct {
	Name   string
	Phone  string
	Status string
}

type IncidentPreview struct {
	TypeCode    string
	TypeName    string
	Features    map[string]any
	Description string
	Victims     int
	Danger      string
}

type NotificationPreview struct {
	Service string
	Status  Reaction
	Mine    bool
}

// ContactPreview is Contact without Phrases (openapi.yaml's Contact
// schema): greeting/ack text is voice-call content a trainee hears when
// they call, not something the catalogue or scoring reference exposes
// ahead of time.
type ContactPreview struct {
	Key    string
	Label  string
	Number string
	Voice  string
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
