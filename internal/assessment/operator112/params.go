package operator112

import "encoding/json"

// addressFieldParam is one rubric.operator112.json ADDRESS_FIELDS.params.
// fields[] entry.
type addressFieldParam struct {
	Path   string  `json:"path"`
	Label  string  `json:"label"`
	Points float64 `json:"points"`
}

// topicParam is one CALLER_TOPICS.params.topics[] entry (112-6's c6).
type topicParam struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Points   float64  `json:"points"`
	Patterns []string `json:"patterns"`
}

// decodeParam re-marshals params[key] (already a generic map[string]any/
// []any tree, decoding rubric.schema.json's free-form "params" object)
// into a typed value — every rubric rule's own params shape is fixed and
// small, so the marshal/unmarshal round trip is simpler and safer than
// hand-walking interface{} assertions. A missing key leaves out at its
// zero value.
func decodeParam[T any](params map[string]any, key string, out *T) error {
	raw, ok := params[key]
	if !ok {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// paramString/paramFloat read a scalar params[key] with a fallback when
// absent — used by the penalty rules' own points/points_per and every
// rule's no_reference_explanation, so a rubric params typo degrades to a
// sane default rather than a panic.
func paramString(params map[string]any, key, fallback string) string {
	if v, ok := params[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

func paramFloat(params map[string]any, key string, fallback float64) float64 {
	switch v := params[key].(type) {
	case float64:
		return v
	case json.Number:
		f, err := v.Float64()
		if err == nil {
			return f
		}
	}
	return fallback
}
