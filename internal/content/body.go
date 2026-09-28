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
	Intake112     *Intake112   `json:"intake112,omitempty"`
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

// Intake112 is immutable prepared content. A legacy call has Script; new
// versions use Dialogue. Neither the dialogue tree nor Reference is sent
// to a trainee.
type Intake112 struct {
	Mode string `json:"mode,omitempty"`
	// CallerMode selects the applicant's own behavior for mode="full_case"
	// (112-5a/ADR-024): "" and CallerModePrepared (the default, and the
	// only value valid for card_only/incoming_call) keep 112-2's scripted
	// question/answer dialogue; CallerModeFreeText opens the trainee's
	// caller-chat window instead — the applicant speaks nothing until the
	// operator writes a message, and a CallerReplier (a stub in 112-5a, a
	// model in 112-5b) answers asynchronously (ADR-024). It never changes
	// which mode/dialogue fields are required on their own — see
	// validateIntake112FullCase.
	CallerMode        string             `json:"caller_mode,omitempty"`
	Call              *Intake112Call     `json:"call,omitempty"`
	Dialogue          *Intake112Dialogue `json:"dialogue,omitempty"`
	RecipientServices []string           `json:"recipient_services,omitempty"`
	Reference         Intake112Reference `json:"reference"`
}

// CallerMode's two allowed values (Intake112.CallerMode's own doc
// comment). "" (the JSON omitted case) behaves exactly like
// CallerModePrepared everywhere this package and internal/training
// compare against it.
const (
	CallerModePrepared = "prepared"
	CallerModeFreeText = "free_text"
)

type Intake112Call struct {
	AON       string   `json:"aon"`
	LocalTime string   `json:"local_time"`
	TimeZone  string   `json:"time_zone"`
	Script    []string `json:"script,omitempty"`
}

// CallerKnowledge describes when a fact can become known to the trainee.
// A fact marked unknown is one the applicant explicitly cannot supply.
//
// Statement, AskPatterns, AskExcludePatterns, AnswerVariants and
// DisclosurePatterns (112-5b) feed the free-text AI caller adapter
// (internal/training/operator112/aicaller): they are meaningless for
// caller_mode="prepared" and validate.go rejects them there. CardPath is
// required for prepared/incoming_call facts (they map onto a card field
// the trainee fills) but optional for free_text facts, which may be
// narrative details (a car's plate number, a bystander's name) with no
// card field of their own — see validateIntake112FreeTextDialogue.
type Intake112Fact struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	CardPath  string `json:"card_path,omitempty"`
	Knowledge string `json:"knowledge"`
	Value     string `json:"value,omitempty"`
	// Statement is how the applicant would say this fact in their own
	// words when the model formulates a reply around it (e.g. "мне
	// сорок лет" for age=40) — distinct from Value, the structured card
	// value. Required alongside a caller profile for initial/on_question
	// facts (unknown facts have nothing to state).
	Statement string `json:"statement,omitempty"`
	// AskPatterns/AskExcludePatterns classify whether an operator message
	// asked about this fact: a RE2 regex (compiled with an implicit
	// "(?i)" prefix) matches, minus any that also match an exclude
	// pattern (e.g. an address question that is actually asking for a
	// landmark). \b is forbidden — Go RE2's \b is ASCII-only and matches
	// nothing useful against Cyrillic text.
	AskPatterns        []string                 `json:"ask_patterns,omitempty"`
	AskExcludePatterns []string                 `json:"ask_exclude_patterns,omitempty"`
	AnswerVariants     []Intake112AnswerVariant `json:"answer_variants,omitempty"`
	// DisclosurePatterns detect this fact already being present in a
	// caller reply (model-authored or scripted), to compute that reply's
	// Reveals without re-asking the model.
	DisclosurePatterns []string `json:"disclosure_patterns,omitempty"`
}

// Intake112AnswerVariant is one scripted phrasing of a fact's answer,
// used verbatim (no model call) when exactly one fact is asked in an
// operator message and When (empty, or a RE2 pattern under the same
// rules as AskPatterns) matches that message.
type Intake112AnswerVariant struct {
	When string `json:"when,omitempty"`
	Text string `json:"text"`
}

// Intake112CallerProfile (112-5b) turns on the AI caller adapter for a
// free_text dialogue: Persona seeds the model's system prompt, Opening is
// the applicant's first line (still said without a model call, same as
// 112-5a's protocol — see validateIntake112FreeTextDialogue's reveals
// check). A free_text dialogue without a Caller profile keeps answering
// through the deterministic stub, same as 112-5a, regardless of
// CALLER_REPLIER (internal/training/operator112/aicaller.Replier).
type Intake112CallerProfile struct {
	Persona string             `json:"persona"`
	Opening Intake112Utterance `json:"opening"`
}

type Intake112Utterance struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Reveals []string `json:"reveals"`
}

type Intake112Question struct {
	ID             string             `json:"id"`
	Text           string             `json:"text"`
	TopicID        string             `json:"topic_id"`
	AvailableAfter []string           `json:"available_after,omitempty"`
	Answer         Intake112Utterance `json:"answer"`
}

type Intake112Dialogue struct {
	Facts     []Intake112Fact     `json:"facts"`
	Initial   Intake112Utterance  `json:"initial"`
	Questions []Intake112Question `json:"questions"`
	// Caller (112-5b) is free_text-only; prepared dialogues must leave it
	// nil (validate.go).
	Caller *Intake112CallerProfile `json:"caller,omitempty"`
}

type Intake112Reference struct {
	ExpectedCard     *Intake112ExpectedCard `json:"expected_card,omitempty"`
	RecipientService string                 `json:"recipient_service"`
	ExpectedTypes    []string               `json:"expected_types,omitempty"`
	CaseDescription  string                 `json:"case_description,omitempty"`
	// ExpectedServices is the closed reference list of service codes a
	// correct notify_services call should include: used for manual
	// review since ADR-023 (slice 112-4), and by 112-6/ADR-026's
	// P_SERVICES penalty. Allowed for card_only and full_case.
	ExpectedServices []string `json:"expected_services,omitempty"`
	// Alternatives (112-6/ADR-026) maps a reference field's own path —
	// the same spelling as ExpectedCard's own field, e.g.
	// "expected_card.address.street" or "expected_card.applicant_name"
	// — to additional values internal/content/normalize also accepts as
	// correct. A path with no entry here has no alternative beyond
	// ExpectedCard's own value (still matched via normalize.TokenSetEqual,
	// so word order and known abbreviations never need an alternative).
	Alternatives map[string][]string `json:"alternatives,omitempty"`
	// ExpectedProfiles (112-6/ADR-026) maps a profile card id
	// (intake_state.catalog's IntakeProfile.ID, e.g. "104") to that
	// card's own field id -> expected answer. A card in ExpectedTypes
	// with no entry here has no reference yet (ADR-026's "эталон
	// отсутствует" rule) — PROFILE_CARDS scores its share as 0, not as
	// not_applicable.
	ExpectedProfiles map[string]map[string]Intake112ExpectedProfileValue `json:"expected_profiles,omitempty"`
	// Scoring (112-6/ADR-026) is operator112/rubric-v2's own reference.
	// scoring override — same Scoring shape and Merge semantics DDS
	// already uses (ADR-013), validated against rubric.operator112.json's
	// criterion ids rather than rubric.default.json's.
	Scoring *Scoring `json:"scoring,omitempty"`
	// DescriptionQuestions (ADR-028, operator112/rubric-v3) are the
	// closed control questions DESCRIPTION_CONTENT's LLM judge answers
	// against the trainee's own complaint field — never the scenario's
	// dialogue or facts. Empty/absent means the same "эталон не задан"
	// zero ADR-026 already applies to ADDRESS_FIELDS/PROFILE_CARDS, and
	// no model call happens at all.
	DescriptionQuestions []Intake112DescriptionQuestion `json:"description_questions,omitempty"`
}

// Intake112DescriptionQuestion is one scenario.schema.json intake112.
// reference.description_questions[] entry — an id stable enough to
// survive round-tripping through assessment_inputs.semantic_input and a
// positively-phrased question text (e.g. "Указано ли, что …?") the judge
// answers yes/no/needs_review against the complaint text alone.
type Intake112DescriptionQuestion struct {
	ID       string `json:"id"`
	Question string `json:"question"`
}

type Intake112ExpectedCard struct {
	ApplicantStatus string `json:"applicant_status"`
	// ApplicantName (112-6/ADR-026) is compared only when the fact was
	// actually disclosed in the conversation (P_APPLICANT_NAME,
	// interpretation §10.3) — an applicant who never gave a name is not
	// penalized for the trainee not knowing it.
	ApplicantName string           `json:"applicant_name,omitempty"`
	Age           int              `json:"age"`
	Address       Intake112Address `json:"address"`
	IncidentType  string           `json:"incident_type"`
	Complaint     string           `json:"complaint"`
	VictimsCount  int              `json:"victims_count"`
}

// Intake112Address is the operator's own IntakeAddress
// (internal/training.IntakeAddress) minus Object/Landmark/Code/
// Descriptive — 112-6/ADR-026 excludes Object and Landmark from scoring
// by the user's own decision ("объект и ориентир не оцениваем"); Code and
// Descriptive have no fixed reference shape to compare against and stay
// unscored for the same reason. Every field is optional: an absent one is
// simply not part of this scenario's reference (ADR-026's "эталон
// отсутствует" rule applies per-field within ADDRESS_FIELDS).
type Intake112Address struct {
	Country   string `json:"country,omitempty"`
	Region    string `json:"region,omitempty"`
	Okrug     string `json:"okrug,omitempty"`
	District  string `json:"district,omitempty"`
	City      string `json:"city,omitempty"`
	Street    string `json:"street,omitempty"`
	House     string `json:"house,omitempty"`
	Building  string `json:"building,omitempty"`
	Structure string `json:"structure,omitempty"`
	Flat      string `json:"flat,omitempty"`
	Entrance  string `json:"entrance,omitempty"`
	Floor     string `json:"floor,omitempty"`
	Landmark  string `json:"landmark,omitempty"`
}

// Intake112ExpectedProfileValue is one profile field's expected answer —
// a single string for the catalog's "single"/"text" field kinds, or a set
// of strings for "multiple" (scenario.schema.json's intake112.reference.
// expected_profiles: string | string[]). Exactly one of Value/Values is
// set after DecodeFile — see UnmarshalJSON.
type Intake112ExpectedProfileValue struct {
	Value  string
	Values []string
}

func (v *Intake112ExpectedProfileValue) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		v.Value, v.Values = s, nil
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	v.Value, v.Values = "", arr
	return nil
}

func (v Intake112ExpectedProfileValue) MarshalJSON() ([]byte, error) {
	if v.Values != nil {
		return json.Marshal(v.Values)
	}
	return json.Marshal(v.Value)
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
// (RFC-001 §7.3). Role (ADR-031) groups a DDS contact as crew, the 112
// control department, the applicant or other; an empty Role is "other".
// Neither Role nor the phrase texts are part of the answer key, so both
// reach the trainee through ProjectContacts (preview.go).
type Contact struct {
	Key     string         `json:"key"`
	Label   string         `json:"label"`
	Number  string         `json:"number"`
	Voice   string         `json:"voice"`
	Role    ContactRole    `json:"role,omitempty"`
	Phrases ContactPhrases `json:"phrases"`
}

// ContactRole is scenario.schema.json's contact.role (ADR-031).
type ContactRole string

const (
	ContactRoleCrew       ContactRole = "crew"
	ContactRoleControl112 ContactRole = "control_112"
	ContactRoleApplicant  ContactRole = "applicant"
	ContactRoleOther      ContactRole = "other"
)

// Valid reports whether r is a known role; empty is valid and means other.
func (r ContactRole) Valid() bool {
	switch r {
	case "", ContactRoleCrew, ContactRoleControl112, ContactRoleApplicant, ContactRoleOther:
		return true
	}
	return false
}

// Effective is r with the schema's default applied.
func (r ContactRole) Effective() ContactRole {
	if r == "" {
		return ContactRoleOther
	}
	return r
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
	Key   string `json:"key"`
	AtS   int    `json:"at_s"`
	Since string `json:"since"`
	// SinceContact is the contact whose first ended outgoing call anchors
	// an event with Since == EventSinceCallEnded (ADR-031); empty otherwise.
	SinceContact string        `json:"since_contact,omitempty"`
	StatusIn     []Reaction    `json:"status_in"`
	Delivery     string        `json:"delivery"`
	From         string        `json:"from"`
	Text         string        `json:"text"`
	Voice        bool          `json:"voice"`
	Spawn        *EventSpawn   `json:"spawn"`
	Expects      *EventExpects `json:"expects"`
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

// EventSpawn is event.spawn — for delivery="spawn_card", the approved
// version of another prepared scenario to create. Variation remains only to
// decode legacy persisted JSON; it is not accepted in the public contract.
type EventSpawn struct {
	Kind        string `json:"kind"`
	ScenarioKey string `json:"scenario_key"`
	Version     int    `json:"version"`
	Variation   string `json:"variation"`
}

// EventExpects is event.expects — the reference reaction to this event,
// used by assessment (slice 6+), not by content itself.
type EventExpects struct {
	Status  Reaction `json:"status"`
	WithinS int      `json:"within_s"`
	Action  string   `json:"action"`
	// CommentFacts are the facts the comment to the status set in reaction
	// to this event should carry (ADR-031); judged in slice DDS-4.
	CommentFacts []string `json:"comment_facts,omitempty"`
}

// EventSinceCallEnded is event.since's call anchor (ADR-031): the end of
// the item's first outgoing call to event.since_contact.
const EventSinceCallEnded = "call_ended"

// Reference is scenario.reference — the closed-book эталон. Never
// projected to a trainee; internal/content/http (C4) exposes it only to
// instructors, as ScenarioReference (openapi.yaml), unmodified from this
// struct.
type Reference struct {
	PrimaryDecision  PrimaryDecision   `json:"primary_decision"`
	ExpectedChain    []Reaction        `json:"expected_chain"`
	Call             Call              `json:"call"`
	FieldCorrections []FieldCorrection `json:"field_corrections"`
	// RequiredContacts is ДДС-3/ADR-032's own C_CALLS input: contact.key
	// values that need a completed outgoing call regardless of Call
	// (e.g. отдел контроля 112 when the scenario calls for it) —
	// on top of, not instead of, Call.Required's own contact.
	RequiredContacts []string `json:"required_contacts,omitempty"`
	PilotGoal        string   `json:"pilot_goal"`
	GuideRefs        []string `json:"guide_refs"`
	Notes            string   `json:"notes"`
	Scoring          *Scoring `json:"scoring"`
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
