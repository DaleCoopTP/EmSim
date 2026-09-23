package operator112

import (
	"slices"
	"strings"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"
)

func typeByID(c *content.IntakeCatalog, id string) (content.IntakeIncidentType, bool) {
	if c == nil {
		return content.IntakeIncidentType{}, false
	}
	for _, t := range c.Types {
		if t.ID == id {
			return t, true
		}
	}
	return content.IntakeIncidentType{}, false
}

func profileByID(c *content.IntakeCatalog, id string) (content.IntakeProfile, bool) {
	if c == nil {
		return content.IntakeProfile{}, false
	}
	for _, p := range c.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return content.IntakeProfile{}, false
}

func activeProfileIDs(c *content.IntakeCatalog, types []string) map[string]bool {
	ids := make(map[string]bool)
	for _, id := range types {
		t, ok := typeByID(c, id)
		if !ok {
			continue
		}
		for _, profileID := range t.ProfileIDs {
			ids[profileID] = true
		}
	}
	return ids
}

func newProfile(def content.IntakeProfile) training.IntakeProfile {
	answers := make(map[string]training.IntakeProfileAnswer)
	for _, f := range def.Fields {
		if f.Kind != "shared" {
			answers[f.ID] = training.IntakeProfileAnswer{State: "unanswered"}
		}
	}
	return training.IntakeProfile{DefinitionID: def.ID, Version: def.Version, Answers: answers}
}

func validProfileAnswer(answer training.IntakeProfileAnswer, field content.IntakeProfileField) bool {
	if answer.State == "unanswered" || answer.State == "unknown" {
		return answer.Value == "" && len(answer.Values) == 0
	}
	if answer.State != "known" {
		return false
	}
	if field.Kind == "text" {
		return len(answer.Value) > 0 && len(answer.Value) <= 1999 && strings.TrimSpace(answer.Value) == answer.Value && len(answer.Values) == 0
	}
	if field.Kind == "single" {
		if len(answer.Values) != 0 {
			return false
		}
		for _, option := range field.Options {
			if answer.Value == option {
				return true
			}
		}
		return false
	}
	if field.Kind == "multiple" {
		if answer.Value != "" || len(answer.Values) == 0 {
			return false
		}
		seen := map[string]bool{}
		for _, value := range answer.Values {
			valid := false
			for _, option := range field.Options {
				if option == value {
					valid = true
				}
			}
			if !valid || seen[value] {
				return false
			}
			seen[value] = true
		}
		return true
	}
	return false
}

func validProfileDraft(card training.IntakeCard, c *content.IntakeCatalog) bool {
	if c == nil || card.IncidentType.State != "unanswered" || card.IncidentType.Value != "" {
		return false
	}
	seenTypes := map[string]bool{}
	for _, id := range card.IncidentTypes {
		if _, ok := typeByID(c, id); !ok || seenTypes[id] {
			return false
		}
		seenTypes[id] = true
	}
	active := activeProfileIDs(c, card.IncidentTypes)
	if len(card.Profiles) != len(active) {
		return false
	}
	for id := range active {
		def, _ := profileByID(c, id)
		profile, ok := card.Profiles[id]
		if !ok || profile.DefinitionID != id || profile.Version != def.Version {
			return false
		}
		count := 0
		for _, f := range def.Fields {
			if f.Kind == "shared" {
				continue
			}
			answer, ok := profile.Answers[f.ID]
			if !ok || !validProfileAnswer(answer, f) {
				return false
			}
			count++
		}
		if len(profile.Answers) != count {
			return false
		}
	}
	return true
}

func serviceSuggestions(card training.IntakeCard, c *content.IntakeCatalog) []training.IntakeServiceSuggestion {
	result := []training.IntakeServiceSuggestion{}
	if c == nil {
		return result
	}
	for _, rule := range c.ServiceRules {
		profile, active := card.Profiles[rule.ProfileID]
		if !active {
			continue
		}
		if rule.FieldID != "" {
			answer := profile.Answers[rule.FieldID]
			if answer.State != "known" || answer.Value != rule.Equals {
				continue
			}
		}
		found := false
		for i := range result {
			if result[i].ServiceCode == rule.ServiceCode {
				result[i].Reasons = append(result[i].Reasons, rule.Reason)
				found = true
				break
			}
		}
		if !found {
			result = append(result, training.IntakeServiceSuggestion{ServiceCode: rule.ServiceCode, Reasons: []string{rule.Reason}})
		}
	}
	return result
}

func decideProfileCase(item training.Item, cmd training.Command, now time.Time) (training.Decision, error) {
	card, state := *item.IntakeCard, *item.IntakeState
	if state.Catalog == nil {
		return reject(item, training.RejectTransitionNotAllowed), nil
	}
	card.IncidentTypes = append([]string(nil), item.IntakeCard.IncidentTypes...)
	card.Profiles = make(map[string]training.IntakeProfile, len(item.IntakeCard.Profiles))
	for id, profile := range item.IntakeCard.Profiles {
		card.Profiles[id] = profile
	}
	state.InactiveProfiles = make(map[string]training.IntakeProfile, len(item.IntakeState.InactiveProfiles))
	for id, profile := range item.IntakeState.InactiveProfiles {
		state.InactiveProfiles[id] = profile
	}
	d := training.Decision{Accepted: true, State: item.State, Reaction: item.Reaction,
		Card: item.Card, IntakeCard: &card, IntakeState: &state}
	switch cmd.Type {
	case training.CommandOpen:
		if item.State != training.ItemOffered {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct{}
		if !payload(cmd.Payload, &p) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		d.State, d.OpenedAt = training.ItemOpened, &now
	case training.CommandAddIncidentType:
		if item.State != training.ItemOpened && item.State != training.ItemInProgress {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct {
			TypeID string `json:"type_id"`
		}
		if !payload(cmd.Payload, &p) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		t, ok := typeByID(state.Catalog, p.TypeID)
		if !ok {
			return reject(item, training.RejectInvalidPayload), nil
		}
		for _, id := range card.IncidentTypes {
			if id == p.TypeID {
				return reject(item, training.RejectInvalidPayload), nil
			}
		}
		card.IncidentTypes = append(card.IncidentTypes, p.TypeID)
		for _, id := range t.ProfileIDs {
			if _, exists := card.Profiles[id]; exists {
				continue
			}
			if cached, exists := state.InactiveProfiles[id]; exists {
				card.Profiles[id] = cached
				delete(state.InactiveProfiles, id)
			} else {
				def, _ := profileByID(state.Catalog, id)
				card.Profiles[id] = newProfile(def)
			}
		}
		state.HasSavedDraft, state.ServiceReview = false, nil
		state.SuggestedServices = serviceSuggestions(card, state.Catalog)
		d.Effect = map[string]any{"type_id": p.TypeID, "active_profiles": activeProfileIDs(state.Catalog, card.IncidentTypes)}
		d.State = training.ItemInProgress
	case training.CommandRemoveIncidentType:
		if item.State != training.ItemOpened && item.State != training.ItemInProgress {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct {
			TypeID string `json:"type_id"`
		}
		if !payload(cmd.Payload, &p) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		found := false
		remaining := make([]string, 0, len(card.IncidentTypes))
		for _, id := range card.IncidentTypes {
			if id == p.TypeID {
				found = true
			} else {
				remaining = append(remaining, id)
			}
		}
		if !found {
			return reject(item, training.RejectInvalidPayload), nil
		}
		card.IncidentTypes = remaining
		active := activeProfileIDs(state.Catalog, remaining)
		for id, profile := range card.Profiles {
			if !active[id] {
				state.InactiveProfiles[id] = profile
				delete(card.Profiles, id)
			}
		}
		state.HasSavedDraft, state.ServiceReview = false, nil
		state.SuggestedServices = serviceSuggestions(card, state.Catalog)
		d.Effect = map[string]any{"type_id": p.TypeID, "active_profiles": active}
	case training.CommandSaveIntakeDraft:
		if item.State != training.ItemOpened && item.State != training.ItemInProgress {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct {
			Draft training.IntakeCard `json:"draft"`
		}
		if !payload(cmd.Payload, &p) || p.Draft.Number != card.Number || p.Draft.AON != card.AON ||
			p.Draft.CallLocalTime != card.CallLocalTime || p.Draft.CallTimeZone != card.CallTimeZone ||
			!slices.Equal(p.Draft.IncidentTypes, card.IncidentTypes) || !training.ValidIntakeCard(p.Draft) ||
			!validProfileDraft(p.Draft, state.Catalog) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		card = p.Draft
		state.HasSavedDraft, state.ServiceReview = true, nil
		state.SuggestedServices = serviceSuggestions(card, state.Catalog)
	case training.CommandReviewServices:
		if !state.HasSavedDraft {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct {
			Services []string `json:"services"`
			Reason   string   `json:"reason"`
		}
		if !payload(cmd.Payload, &p) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		allowed := map[string]bool{}
		for _, rule := range state.Catalog.ServiceRules {
			allowed[rule.ServiceCode] = true
		}
		selected := map[string]bool{}
		for _, code := range p.Services {
			if !allowed[code] || selected[code] {
				return reject(item, training.RejectInvalidPayload), nil
			}
			selected[code] = true
		}
		suggested := map[string]bool{}
		for _, s := range state.SuggestedServices {
			suggested[s.ServiceCode] = true
		}
		changed := len(selected) != len(suggested)
		for code := range selected {
			if !suggested[code] {
				changed = true
			}
		}
		if changed && strings.TrimSpace(p.Reason) == "" {
			return reject(item, training.RejectInvalidPayload), nil
		}
		if len(p.Reason) > 1000 || strings.TrimSpace(p.Reason) != p.Reason {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.ServiceReview = &training.IntakeServiceReview{Suggested: append([]training.IntakeServiceSuggestion(nil), state.SuggestedServices...),
			Selected: append([]string{}, p.Services...), Reason: p.Reason, ReviewedAt: now}
		d.Effect = map[string]any{"suggested": state.ServiceReview.Suggested, "selected": state.ServiceReview.Selected, "reason": state.ServiceReview.Reason}
	case training.CommandCompleteProfileCase:
		if len(card.IncidentTypes) == 0 || !state.HasSavedDraft || state.ServiceReview == nil ||
			(item.State != training.ItemOpened && item.State != training.ItemInProgress) {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct{}
		if !payload(cmd.Payload, &p) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		reason := training.CloseCompleted
		d.Close, d.State = &reason, training.ItemClosed
	default:
		return reject(item, training.RejectTransitionNotAllowed), nil
	}
	d.IntakeCard, d.IntakeState = &card, &state
	return d, nil
}
