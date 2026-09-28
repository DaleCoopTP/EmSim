package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// IntakeCatalog is independent of a case. Its complete immutable version is
// copied into each offered card-only item so later imports cannot change a run.
type IntakeCatalog struct {
	Version      int                  `json:"version"`
	Types        []IntakeIncidentType `json:"types"`
	Profiles     []IntakeProfile      `json:"profiles"`
	ServiceRules []IntakeServiceRule  `json:"service_rules"`
}

type IntakeIncidentType struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ProfileIDs []string `json:"profile_ids"`
}

type IntakeProfile struct {
	ID      string               `json:"id"`
	Version int                  `json:"version"`
	Name    string               `json:"name"`
	Fields  []IntakeProfileField `json:"fields"`
}

type IntakeProfileField struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"` // single, multiple, text, shared
	Options []string `json:"options,omitempty"`
	Shared  string   `json:"shared,omitempty"` // no_on_site or no_access
	// VisibleWhen makes a field conditional on an earlier single/multiple
	// field of the same card, as in АРМ-112's own 101 card, where the
	// "Где" answer picks which fire-sign and object questions follow. A
	// hidden field is never answered: the draft keeps it "unanswered".
	VisibleWhen *IntakeFieldCondition `json:"visible_when,omitempty"`
}

// IntakeFieldCondition holds when the referenced field is known and its
// value (or, for a multiple field, any of its values) is one of AnyOf.
type IntakeFieldCondition struct {
	FieldID string   `json:"field_id"`
	AnyOf   []string `json:"any_of"`
}

// IntakeFieldVisible reports whether field is shown for the given known
// answers of its card: answers maps a field id to its selected options
// (nil or empty when the field is not answered with a known value).
// Visibility is transitive — a condition on a hidden field is false,
// since a hidden field is never answered.
func IntakeFieldVisible(field IntakeProfileField, answers func(fieldID string) []string) bool {
	if field.VisibleWhen == nil {
		return true
	}
	for _, value := range answers(field.VisibleWhen.FieldID) {
		for _, want := range field.VisibleWhen.AnyOf {
			if value == want {
				return true
			}
		}
	}
	return false
}

type IntakeServiceRule struct {
	ID          string `json:"id"`
	ProfileID   string `json:"profile_id"`
	FieldID     string `json:"field_id,omitempty"`
	Equals      string `json:"equals,omitempty"`
	ServiceCode string `json:"service_code"`
	Reason      string `json:"reason"`
}

func DecodeIntakeCatalog(r io.Reader) (IntakeCatalog, error) {
	var catalog IntakeCatalog
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(&catalog); err != nil {
		return IntakeCatalog{}, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return IntakeCatalog{}, fmt.Errorf("intake catalog: trailing JSON")
	}
	if err := ValidateIntakeCatalog(catalog); err != nil {
		return IntakeCatalog{}, err
	}
	return catalog, nil
}

func ValidateIntakeCatalog(c IntakeCatalog) error {
	if c.Version < 1 || len(c.Types) == 0 || len(c.Profiles) == 0 {
		return fmt.Errorf("intake catalog: empty or invalid version")
	}
	profiles := make(map[string]IntakeProfile, len(c.Profiles))
	for _, p := range c.Profiles {
		if p.ID == "" || p.Version < 1 || p.Name == "" || len(p.Fields) == 0 || profiles[p.ID].ID != "" {
			return fmt.Errorf("intake catalog: invalid/duplicate profile %q", p.ID)
		}
		fields := map[string]IntakeProfileField{}
		for _, f := range p.Fields {
			if f.ID == "" || f.Label == "" || fields[f.ID].ID != "" {
				return fmt.Errorf("intake catalog: invalid/duplicate field %q", f.ID)
			}
			if err := validateFieldCondition(f, fields); err != nil {
				return err
			}
			fields[f.ID] = f
			switch f.Kind {
			case "single", "multiple":
				if len(f.Options) == 0 {
					return fmt.Errorf("intake catalog: no options for %q", f.ID)
				}
				options := map[string]bool{}
				for _, option := range f.Options {
					if option == "" || options[option] {
						return fmt.Errorf("intake catalog: invalid/duplicate option for %q", f.ID)
					}
					options[option] = true
				}
			case "text":
				if len(f.Options) != 0 {
					return fmt.Errorf("intake catalog: text options for %q", f.ID)
				}
			case "shared":
				if f.Shared != "no_on_site" && f.Shared != "no_access" {
					return fmt.Errorf("intake catalog: invalid shared field %q", f.ID)
				}
			default:
				return fmt.Errorf("intake catalog: invalid kind %q", f.Kind)
			}
		}
		profiles[p.ID] = p
	}
	types := map[string]bool{}
	for _, t := range c.Types {
		if t.ID == "" || t.Name == "" || len(t.ProfileIDs) == 0 || types[t.ID] {
			return fmt.Errorf("intake catalog: invalid/duplicate type %q", t.ID)
		}
		types[t.ID] = true
		seen := map[string]bool{}
		for _, id := range t.ProfileIDs {
			if profiles[id].ID == "" || seen[id] {
				return fmt.Errorf("intake catalog: invalid profile reference %q", id)
			}
			seen[id] = true
		}
	}
	rules := map[string]bool{}
	for _, r := range c.ServiceRules {
		if r.ID == "" || r.ServiceCode == "" || r.Reason == "" || rules[r.ID] || profiles[r.ProfileID].ID == "" {
			return fmt.Errorf("intake catalog: invalid service rule %q", r.ID)
		}
		rules[r.ID] = true
		if r.FieldID != "" {
			found := false
			for _, f := range profiles[r.ProfileID].Fields {
				if f.ID != r.FieldID || f.Kind != "single" {
					continue
				}
				for _, option := range f.Options {
					if option == r.Equals {
						found = true
					}
				}
			}
			if !found {
				return fmt.Errorf("intake catalog: invalid rule field %q", r.FieldID)
			}
		} else if r.Equals != "" {
			return fmt.Errorf("intake catalog: unconditional rule has equals")
		}
	}
	return nil
}

// validateFieldCondition requires a condition to point at an earlier
// single/multiple field of the same card and to name only its options,
// so a condition can never form a cycle or wait for an impossible value.
func validateFieldCondition(f IntakeProfileField, earlier map[string]IntakeProfileField) error {
	if f.VisibleWhen == nil {
		return nil
	}
	if f.Kind == "shared" {
		return fmt.Errorf("intake catalog: shared field %q cannot be conditional", f.ID)
	}
	parent, ok := earlier[f.VisibleWhen.FieldID]
	if !ok || (parent.Kind != "single" && parent.Kind != "multiple") || len(f.VisibleWhen.AnyOf) == 0 {
		return fmt.Errorf("intake catalog: invalid condition for %q", f.ID)
	}
	for _, want := range f.VisibleWhen.AnyOf {
		found := false
		for _, option := range parent.Options {
			found = found || option == want
		}
		if !found {
			return fmt.Errorf("intake catalog: condition for %q names unknown option %q", f.ID, want)
		}
	}
	return nil
}

func EqualIntakeCatalog(a, b IntakeCatalog) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
