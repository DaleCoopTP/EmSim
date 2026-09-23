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
		fields := map[string]bool{}
		for _, f := range p.Fields {
			if f.ID == "" || f.Label == "" || fields[f.ID] {
				return fmt.Errorf("intake catalog: invalid/duplicate field %q", f.ID)
			}
			fields[f.ID] = true
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

func EqualIntakeCatalog(a, b IntakeCatalog) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
