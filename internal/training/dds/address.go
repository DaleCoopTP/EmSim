package dds

import (
	"strings"

	"emsim/internal/content"
)

// formatPilotAddressText rebuilds card.address.text after set_card_field
// changes card.address.okrug, so the correction is visible in the one
// field the trainee actually reads instead of only in the structured
// okrug value. It reproduces exactly the convention slice 2's pilot
// scenario files were authored with (seed/scenarios/pilot-tree-0{1,2}.
// json): "{country}, {city}, ({okrug}, {district}), {street}, {house},
// к. {building}, под. {entrance}". This is a pilot-specific convention
// (ADR-017), not a general address formatter for arbitrary future
// scenarios — set_card_field itself is restricted to this one field on
// these two scenarios, and this function is its only caller.
func formatPilotAddressText(a content.Address) string {
	var parts []string
	if a.Country != "" {
		parts = append(parts, a.Country)
	}
	if a.City != "" {
		parts = append(parts, a.City)
	}
	if okrugDistrict := joinNonEmpty(", ", a.Okrug, a.District); okrugDistrict != "" {
		parts = append(parts, "("+okrugDistrict+")")
	}
	if a.Street != "" {
		parts = append(parts, a.Street)
	}
	if a.House != "" {
		parts = append(parts, a.House)
	}
	if a.Building != "" {
		parts = append(parts, "к. "+a.Building)
	}
	if a.Entrance != "" {
		parts = append(parts, "под. "+a.Entrance)
	}
	return strings.Join(parts, ", ")
}

func joinNonEmpty(sep string, values ...string) string {
	kept := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" {
			kept = append(kept, v)
		}
	}
	return strings.Join(kept, sep)
}
