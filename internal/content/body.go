package content

import (
	"encoding/json"

	"github.com/google/uuid"
)

// Body is scenario.schema.json's ScenarioBody — a scenario version's
// immutable content (scenario_versions.body). Decoded by DecodeFile from
// a scenario-file.schema.json document's "body"; every field here mirrors
// the schema one-to-one, so a new schema property needs a matching Go
// field before Validate or the http/preview layers can see it.
type Body struct {
	Schema        string       `json:"schema"`
	TargetService string       `json:"target_service"`
	Card          Card         `json:"card"`
	Contacts      []Contact    `json:"contacts"`
	Events        []Event      `json:"events"`
	Reference     Reference    `json:"reference"`
	Hints         []Hint       `json:"hints"`
	Generation    *Generation  `json:"generation"`
	Difficulty    int          `json:"difficulty"`
	ExerciseType  ExerciseType `json:"exercise_type"`
}

// Card is scenario.schema.json's $defs.card — the incoming card as the
// dispatcher sees it in АРМ-112.
type Card struct {
	Number              string              `json:"number"`
	RegisteredAtOffsetS int                 `json:"registered_at_offset_s"`
	Applicant           Applicant           `json:"applicant"`
	Address             Address             `json:"address"`
	Incident            Incident            `json:"incident"`
	NotificationList    []NotificationEntry `json:"notification_list"`
	Phones              Phones              `json:"phones"`
	Channel             string              `json:"channel"`
}

// Applicant is card.applicant. Status is one of the six 112 applicant
// statuses (schema enum) or empty when the scenario does not specify one.
type Applicant struct {
	Name   string `json:"name"`
	Phone  string `json:"phone"`
	Status string `json:"status"`
}

// Address is card.address — every field but district/street/house is
// optional and left at its zero value ("") when the scenario has nothing
// there; this is also internal/content.CardPreview's Address, so a field
// added here must be added to the OpenAPI Address schema and CardPreview's
// allowlist projection (preview.go) too.
type Address struct {
	District    string `json:"district"`
	Street      string `json:"street"`
	House       string `json:"house"`
	Building    string `json:"building"`
	Entrance    string `json:"entrance"`
	Floor       string `json:"floor"`
	Flat        string `json:"flat"`
	Landmark    string `json:"landmark"`
	Text        string `json:"text"`
	Country     string `json:"country"`
	Region      string `json:"region"`
	City        string `json:"city"`
	Okrug       string `json:"okrug"`
	Object      string `json:"object"`
	Structure   string `json:"structure"`
	Code        string `json:"code"`
	Descriptive string `json:"descriptive"`
}

// Incident is card.incident. Features holds the classifier's опросная
// карта answers as decoded by encoding/json (string/bool/float64) — it is
// display-only content, never compared by identity, so the float64
// widening standard decoding does to JSON numbers here is harmless.
type Incident struct {
	TypeCode    string         `json:"type_code"`
	TypeName    string         `json:"type_name"`
	Features    map[string]any `json:"features"`
	Description string         `json:"description"`
	Victims     int            `json:"victims"`
	Danger      string         `json:"danger"`
}

// NotificationEntry is one card.notification_list row.
type NotificationEntry struct {
	Service string   `json:"service"`
	Status  Reaction `json:"status"`
	Mine    bool     `json:"mine"`
}

// Phones is card.phones.
type Phones struct {
	AON      string `json:"aon"`
	Provided string `json:"provided"`
	OnSite   string `json:"on_site"`
}

// Contact is one scenario.contacts entry — a simulated phone contact
// (RFC-001 §7.3). Phrases is voice-only (slice 5+); CardPreview's
// projection deliberately drops it (preview.go).
type Contact struct {
	Key     string         `json:"key"`
	Label   string         `json:"label"`
	Number  string         `json:"number"`
	Voice   string         `json:"voice"`
	Phrases ContactPhrases `json:"phrases"`
}

type ContactPhrases struct {
	Greeting string `json:"greeting"`
	Ack      string `json:"ack"`
}

// Hint is one scenario.hints entry — intro-mode guidance (slice 8).
type Hint struct {
	Step     string `json:"step"`
	Text     string `json:"text"`
	GuideRef string `json:"guide_ref"`
}

// Generation is scenario.generation — provenance for an LLM-authored
// scenario (slice 11); a manually prepared file (this slice) omits it.
type Generation struct {
	Model          string     `json:"model"`
	PromptVersion  string     `json:"prompt_version"`
	TicketID       *uuid.UUID `json:"ticket_id"`
	ClassifierCode string     `json:"classifier_code"`
}

// Event is one scenario.events entry — a timeline event (RFC-001 §7.2).
// Voice defaults to true per the schema ("Озвучить при утверждении") when
// the file omits it; UnmarshalJSON applies that default explicitly since
// encoding/json otherwise leaves a missing bool at its zero value (false).
type Event struct {
	Key      string        `json:"key"`
	AtS      int           `json:"at_s"`
	Since    string        `json:"since"`
	StatusIn []Reaction    `json:"status_in"`
	Delivery string        `json:"delivery"`
	From     string        `json:"from"`
	Text     string        `json:"text"`
	Voice    bool          `json:"voice"`
	Spawn    *EventSpawn   `json:"spawn"`
	Expects  *EventExpects `json:"expects"`
}

func (e *Event) UnmarshalJSON(data []byte) error {
	type alias Event
	aux := struct {
		Voice *bool `json:"voice"`
		*alias
	}{alias: (*alias)(e)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Voice == nil {
		e.Voice = true
	} else {
		e.Voice = *aux.Voice
	}
	return nil
}

// EventSpawn is event.spawn — for delivery="spawn_card", what card to
// create (a duplicate of the same incident, or another prepared
// scenario's approved version).
type EventSpawn struct {
	Kind              string     `json:"kind"`
	ScenarioVersionID *uuid.UUID `json:"scenario_version_id"`
	Variation         string     `json:"variation"`
}

// EventExpects is event.expects — the reference reaction to this event,
// used by assessment (slice 6+), not by content itself.
type EventExpects struct {
	Status  Reaction `json:"status"`
	WithinS int      `json:"within_s"`
	Action  string   `json:"action"`
}

// Reference is scenario.reference — the closed-book эталон. Never
// projected to a trainee; internal/content/http (C4) exposes it only to
// instructors, as ScenarioReference (openapi.yaml), unmodified from this
// struct.
type Reference struct {
	PrimaryDecision  PrimaryDecision   `json:"primary_decision"`
	ExpectedChain    []Reaction        `json:"expected_chain"`
	Call             Call              `json:"call"`
	FieldCorrections []FieldCorrection `json:"field_corrections"`
	PilotGoal        string            `json:"pilot_goal"`
	GuideRefs        []string          `json:"guide_refs"`
	Notes            string            `json:"notes"`
	Scoring          *Scoring          `json:"scoring"`
}

type PrimaryDecision struct {
	Status             Reaction `json:"status"`
	ReasonTags         []string `json:"reason_tags"`
	CommentRequired    bool     `json:"comment_required"`
	CommentMustMention []string `json:"comment_must_mention"`
}

type Call struct {
	Required     bool     `json:"required"`
	To           string   `json:"to"`
	MustMention  []string `json:"must_mention"`
	BeforeStatus Reaction `json:"before_status"`
}

// FieldCorrection is one reference.field_corrections entry — the slice-2
// pilot extension (scenario.schema.json's $defs.reference.field_corrections
// doc comment): a card field the trainee must correct before a given
// status. Path is JSON-Schema-restricted to a single allowlisted pointer
// today ("/card/address/okrug"); ExpectedValue is deliberately not
// required to differ from the card's actual value at the domain level —
// that mismatch is the error the trainee is meant to find, not one
// Validate enforces (see Validate's doc comment).
type FieldCorrection struct {
	Path          string   `json:"path"`
	ExpectedValue string   `json:"expected_value"`
	BeforeStatus  Reaction `json:"before_status"`
}

// Scoring is reference.scoring — ADR-013's per-case rubric overrides:
// only the deltas from rubric.default.json, never a replacement rubric.
type Scoring struct {
	Weights  map[string]float64 `json:"weights"`
	Critical []string           `json:"critical"`
	Disabled []string           `json:"disabled"`
	Note     string             `json:"note"`
}
