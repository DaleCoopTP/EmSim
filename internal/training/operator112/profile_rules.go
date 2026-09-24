package operator112

import (
	"slices"
	"strconv"
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

// decideProfileFlow handles every command for both card_only and
// full_case: they share incident-type/profile-card/notify_services
// mechanics (ADR-023) in full, and full_case additionally layers the
// same incoming-call commands rules.go's own decideIncomingCall uses —
// duplicated here rather than shared as a helper only where the two
// truly diverge (card_only never reaches these cases at all, since
// state.Mode != "full_case" rejects them up front).
func (e exercise) decideProfileFlow(item training.Item, cmd training.Command, now time.Time) (training.Decision, error) {
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
	state.Transcript = append(make([]training.IntakeLine, 0, len(item.IntakeState.Transcript)), item.IntakeState.Transcript...)
	state.AskedQuestionIDs = append([]string(nil), item.IntakeState.AskedQuestionIDs...)
	// CallerTurns (112-5a/ADR-024) needs the same defensive copy as
	// Transcript/AskedQuestionIDs above — item.IntakeState is a shallow
	// read from the store, so its slice's backing array must not be
	// mutated in place by this decision.
	state.CallerTurns = append(make([]training.IntakeCallerTurn, 0, len(item.IntakeState.CallerTurns)), item.IntakeState.CallerTurns...)
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
	case training.CommandAnswerIncoming:
		if state.Mode != "full_case" || item.State != training.ItemOpened || state.CallStatus != "ringing" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus, state.AnsweredAt = "connected", &now
		state.Transcript = []training.IntakeLine{}
		if state.CallerMode == content.CallerModeFreeText {
			// 112-5a/ADR-024: the applicant says nothing until the
			// operator writes the first chat message — no scripted
			// greeting line, unlike the prepared dialogue below.
		} else {
			if item.IntakeDialogue == nil {
				return reject(item, training.RejectTransitionNotAllowed), nil
			}
			turn, ok := e.caller.Turn(callerRequest(item, ""))
			if !ok {
				return reject(item, training.RejectTransitionNotAllowed), nil
			}
			appendLine(&state, item, cmd, now, turn.Answer.ID, "caller", turn.Answer.Text, "", "", turn.Answer.Reveals)
		}
		d.State = training.ItemInProgress
	case training.CommandAskIntakeQuestion:
		if state.Mode != "full_case" || state.CallerMode == content.CallerModeFreeText ||
			state.CallStatus != "connected" || item.IntakeDialogue == nil {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct {
			QuestionID string `json:"question_id"`
		}
		if !payload(cmd.Payload, &p) || p.QuestionID == "" {
			return reject(item, training.RejectInvalidPayload), nil
		}
		turn, ok := e.caller.Turn(callerRequest(item, p.QuestionID))
		if !ok || turn.Question == nil {
			return reject(item, training.RejectInvalidPayload), nil
		}
		question := turn.Question
		appendLine(&state, item, cmd, now, question.ID, "operator", question.Text, question.ID, question.TopicID, nil)
		appendLine(&state, item, cmd, now, turn.Answer.ID, "caller", turn.Answer.Text, question.ID, question.TopicID, turn.Answer.Reveals)
		asked := false
		for _, id := range state.AskedQuestionIDs {
			asked = asked || id == question.ID
		}
		if !asked {
			state.AskedQuestionIDs = append(state.AskedQuestionIDs, question.ID)
		}
	case training.CommandHoldIncoming:
		if state.Mode != "full_case" || state.CallStatus != "connected" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus = "held"
		cancelPendingCallerTurn(&state, now, "held")
	case training.CommandResumeIncoming:
		if state.Mode != "full_case" || state.CallStatus != "held" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus = "connected"
	case training.CommandEndIncoming:
		if state.Mode != "full_case" || (state.CallStatus != "connected" && state.CallStatus != "held") {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus, state.EndedAt = "ended", &now
		cancelPendingCallerTurn(&state, now, "ended")
	case training.CommandMarkNoContact:
		if state.Mode != "full_case" || item.State != training.ItemOpened || state.CallStatus != "ringing" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus, state.EndedAt = "ended", &now
		reason := training.CloseNoContact
		d.Close, d.State = &reason, training.ItemClosed
	case training.CommandMarkCallDropped:
		if state.Mode != "full_case" || (state.CallStatus != "connected" && state.CallStatus != "held") || state.Notified {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var empty struct{}
		if !payload(cmd.Payload, &empty) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.CallStatus, state.EndedAt = "ended", &now
		cancelPendingCallerTurn(&state, now, "dropped")
		reason := training.CloseCallDropped
		d.Close, d.State = &reason, training.ItemClosed
	case training.CommandAddIncidentType:
		if (item.State != training.ItemOpened && item.State != training.ItemInProgress) || state.Notified {
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
		if (item.State != training.ItemOpened && item.State != training.ItemInProgress) || state.Notified {
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
		if (item.State != training.ItemOpened && item.State != training.ItemInProgress) ||
			state.Notified || state.CallStatus == "ringing" {
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
		if state.Finale == "notify" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		if !state.HasSavedDraft {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct {
			Services []string `json:"services"`
			Reason   string   `json:"reason"`
		}
		if !payload(cmd.Payload, &p) || !validServiceSelection(state.Catalog, state.SuggestedServices, p.Services, p.Reason, false) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		state.ServiceReview = &training.IntakeServiceReview{Suggested: append([]training.IntakeServiceSuggestion(nil), state.SuggestedServices...),
			Selected: append([]string{}, p.Services...), Reason: p.Reason, ReviewedAt: now}
		d.Effect = map[string]any{"suggested": state.ServiceReview.Suggested, "selected": state.ServiceReview.Selected, "reason": state.ServiceReview.Reason}
	case training.CommandCompleteProfileCase:
		if state.Finale == "notify" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
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
	case training.CommandNotifyServices:
		// ADR-023: "Сохранить → оповестить и сохранить карточку" — one
		// action that notifies the whole service list and, unlike the
		// legacy review_service_selection, closes the draft for edits
		// immediately (save_intake_draft/add_incident_type/
		// remove_incident_type all reject once state.Notified is true).
		if state.Finale != "notify" || len(card.IncidentTypes) == 0 || !state.HasSavedDraft || state.Notified {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct {
			Services []string `json:"services"`
			Reason   string   `json:"reason"`
		}
		if !payload(cmd.Payload, &p) || !validServiceSelection(state.Catalog, state.SuggestedServices, p.Services, p.Reason, true) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		suggested := map[string]bool{}
		for _, s := range state.SuggestedServices {
			suggested[s.ServiceCode] = true
		}
		services := make([]training.IntakeNotificationService, len(p.Services))
		for i, code := range p.Services {
			services[i] = training.IntakeNotificationService{ServiceCode: code, Suggested: suggested[code]}
		}
		state.Notified = true
		d.IntakeNotification = &training.IntakeNotification{Services: services, Reason: p.Reason, CardSnapshot: card, NotifiedAt: now}
		d.Effect = map[string]any{"services": p.Services, "reason": p.Reason}
	case training.CommandSendCallerMessage:
		// 112-5a/ADR-024: the operator's own half of one caller-chat
		// turn. The reply itself is not decided here — Decide stays
		// pure (Exercise's own doc comment) and a caller.reply task,
		// enqueued by the application service from
		// d.CallerTurnRequested in the same transaction as this
		// command, resolves the turn asynchronously via
		// ApplyCallerReply.
		if state.Mode != "full_case" || state.CallerMode != content.CallerModeFreeText || state.CallStatus != "connected" {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		if hasPendingCallerTurn(state.CallerTurns) {
			return reject(item, training.RejectTransitionNotAllowed), nil
		}
		var p struct {
			Text string `json:"text"`
		}
		if !payload(cmd.Payload, &p) {
			return reject(item, training.RejectInvalidPayload), nil
		}
		text := strings.TrimSpace(p.Text)
		if text == "" || len(text) > 500 {
			return reject(item, training.RejectInvalidPayload), nil
		}
		// appendLine computes this same id from len(state.Transcript) —
		// captured before the call so the new turn can reference it.
		lineID := cmd.CommandID.String() + ":" + strconv.Itoa(len(state.Transcript)+1)
		appendLine(&state, item, cmd, now, "", "operator", text, "", "", nil)
		turn := len(state.CallerTurns) + 1
		state.CallerTurns = append(state.CallerTurns, training.IntakeCallerTurn{
			Turn: turn, OperatorLineID: lineID, Status: training.CallerTurnPending, RequestedAt: now,
		})
		d.CallerTurnRequested = &turn
	case training.CommandCompleteIntake:
		// card_only never has a call (CallStatus stays not_applicable);
		// full_case requires the caller conversation to have ended first,
		// the same precondition dispatch_intake's incoming_call route
		// enforces (decideIncomingCall's own CommandCompleteIntake case).
		callDone := state.CallStatus == "not_applicable" || (state.Mode == "full_case" && state.CallStatus == "ended")
		if state.Finale != "notify" || !state.Notified || !callDone ||
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

// validServiceSelection is the shared "final service list" check for
// both review_service_selection (kept for pre-ADR-023 card_only items)
// and notify_services: every code must be one the catalog's rules can
// produce, without duplicates, and a selection that differs from
// state.SuggestedServices needs a non-empty reason (RFC-001's "изменение
// сохраняет исходное предложение и итоговый выбор"). requireNonEmpty is
// false for review_service_selection (its existing contract allows an
// empty list) and true for notify_services (ADR-023: "непустой список").
func validServiceSelection(catalog *content.IntakeCatalog, suggested []training.IntakeServiceSuggestion, services []string, reason string, requireNonEmpty bool) bool {
	if catalog == nil {
		return false
	}
	if requireNonEmpty && len(services) == 0 {
		return false
	}
	allowed := map[string]bool{}
	for _, rule := range catalog.ServiceRules {
		allowed[rule.ServiceCode] = true
	}
	selected := map[string]bool{}
	for _, code := range services {
		if !allowed[code] || selected[code] {
			return false
		}
		selected[code] = true
	}
	suggestedSet := map[string]bool{}
	for _, s := range suggested {
		suggestedSet[s.ServiceCode] = true
	}
	changed := len(selected) != len(suggestedSet)
	for code := range selected {
		if !suggestedSet[code] {
			changed = true
		}
	}
	if changed && strings.TrimSpace(reason) == "" {
		return false
	}
	return len(reason) <= 1000 && strings.TrimSpace(reason) == reason
}

// hasPendingCallerTurn reports whether a free-text caller chat has a
// turn still awaiting its caller.reply (112-5a/ADR-024) —
// send_caller_message rejects a new message while one is pending, since
// the applicant answers strictly in order.
func hasPendingCallerTurn(turns []training.IntakeCallerTurn) bool {
	for _, t := range turns {
		if t.Status == training.CallerTurnPending {
			return true
		}
	}
	return false
}

// cancelPendingCallerTurn resolves any still-pending caller turn to
// CallerTurnCancelled (112-5a/ADR-024). hold_incoming, end_incoming and
// mark_call_dropped all call this before their own effect: once the
// call is no longer connected, a caller.reply answer for that turn
// would have nowhere to be shown, and ApplyCallerReply's own pending
// check makes a worker that resolves it after this point a no-op
// regardless — this is the trainee-visible half of that same guarantee.
func cancelPendingCallerTurn(state *training.IntakeState, now time.Time, reason string) {
	for i := range state.CallerTurns {
		if state.CallerTurns[i].Status == training.CallerTurnPending {
			resolvedAt := now
			state.CallerTurns[i].Status = training.CallerTurnCancelled
			state.CallerTurns[i].ResolvedAt = &resolvedAt
			state.CallerTurns[i].Reason = reason
		}
	}
}
