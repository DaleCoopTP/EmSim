package operator112

import (
	"emsim/internal/content"
	"emsim/internal/training"
)

// knownValue returns f's value if the trainee actually filled it
// (state=known), else "" — the same "absence vs explicit unknown"
// distinction IntakeField itself documents; only a known value is ever
// worth comparing against a reference.
func knownValue(f training.IntakeField) string {
	if f.State == "known" {
		return f.Value
	}
	return ""
}

// addressExpectedValue reads one field of content.Intake112Address by
// its rubric.operator112.json params path (e.g. "street") — the same
// vocabulary internal/content/validate.go's expectedCardFieldValue uses
// for expected_card.address.<path>, kept as a separate, smaller switch
// here since this one never needs the non-address fields (applicant_
// name/age/...) that function also handles.
func addressExpectedValue(a content.Intake112Address, path string) string {
	switch path {
	case "country":
		return a.Country
	case "region":
		return a.Region
	case "okrug":
		return a.Okrug
	case "district":
		return a.District
	case "city":
		return a.City
	case "street":
		return a.Street
	case "house":
		return a.House
	case "building":
		return a.Building
	case "structure":
		return a.Structure
	case "flat":
		return a.Flat
	case "entrance":
		return a.Entrance
	case "floor":
		return a.Floor
	case "landmark":
		return a.Landmark
	}
	return ""
}

// addressActualField reads one field of the trainee's own
// training.IntakeAddress by the same path vocabulary.
func addressActualField(a training.IntakeAddress, path string) training.IntakeField {
	switch path {
	case "country":
		return a.Country
	case "region":
		return a.Region
	case "okrug":
		return a.Okrug
	case "district":
		return a.District
	case "city":
		return a.City
	case "street":
		return a.Street
	case "house":
		return a.House
	case "building":
		return a.Building
	case "structure":
		return a.Structure
	case "flat":
		return a.Flat
	case "entrance":
		return a.Entrance
	case "floor":
		return a.Floor
	case "landmark":
		return a.Landmark
	}
	return training.IntakeField{}
}

// alternativesFor reads reference.alternatives for one expected_card.*
// path (scenario-author-facing spelling, ADR-026) — "" alternatives is
// simply not present, never an error, since alternatives is always
// optional.
func alternativesFor(ref content.Intake112Reference, path string) []string {
	return ref.Alternatives["expected_card."+path]
}
