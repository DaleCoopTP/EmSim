package dds

import (
	"regexp"
	"strings"

	"emsim/internal/assessment"
	"emsim/internal/training"
)

// addressRule is G_ADDRESS: written comments/call summaries must not
// contradict the card's own street/house (ADR-013: normalized address
// components, not string similarity; ambiguous extraction must give
// unavailable, never a false not_met). This is a conservative heuristic,
// not a real address parser — it only ever claims a conflict when a
// number token appears alongside the card's own street name and does not
// match the card's house number; anything it cannot confidently classify
// comes back unavailable instead of guessing.
func addressRule(ev training.EvidenceBody, c assessment.RubricCriterion) assessment.CriterionResult {
	street := strings.TrimSpace(ev.FinalCard.Address.Street)
	house := strings.TrimSpace(ev.FinalCard.Address.House)
	if street == "" && house == "" {
		return na(c)
	}
	texts := addressCheckTexts(ev, c.Params)
	if len(texts) == 0 {
		return na(c)
	}

	var mentionsAddress, conflict, ambiguous bool
	for _, text := range texts {
		normalized := strings.ToLower(text)
		streetMentioned := street != "" && strings.Contains(normalized, strings.ToLower(street))
		numbers := extractNumberTokens(normalized)
		if !streetMentioned && len(numbers) == 0 {
			continue
		}
		mentionsAddress = true
		if !streetMentioned {
			// A bare number with no street context (a victim count, a
			// phone digit, a floor) is not evidence of the address at
			// all — never guess that it is.
			ambiguous = true
			continue
		}
		if house == "" || len(numbers) == 0 {
			continue
		}
		if !containsToken(numbers, house) {
			conflict = true
		}
	}

	switch {
	case !mentionsAddress:
		return na(c)
	case conflict:
		return notMet(c, "в тексте указан другой номер дома, чем в карточке")
	case ambiguous:
		return unavailable(c, "не удалось надёжно сопоставить адрес в тексте с карточкой")
	default:
		return met(c, "адрес в тексте совпадает с карточкой")
	}
}

// addressCheckTexts reads the rubric criterion's own "fields" param
// (rubric.default.json's G_ADDRESS: ["comments","call_summary"]) to
// decide which evidence sources to combine — data-driven, so a future
// reference.scoring override narrowing the field set needs no code
// change here.
func addressCheckTexts(ev training.EvidenceBody, params map[string]any) []string {
	fields, _ := params["fields"].([]any)
	includeComments, includeCallSummary := true, true
	if len(fields) > 0 {
		includeComments, includeCallSummary = false, false
		for _, f := range fields {
			switch f {
			case "comments":
				includeComments = true
			case "call_summary":
				includeCallSummary = true
			}
		}
	}
	var texts []string
	if includeComments {
		for _, comment := range ev.Comments {
			texts = append(texts, comment.Text)
		}
	}
	if includeCallSummary {
		for _, call := range ev.Calls {
			if call.Outgoing() && call.Summary != nil {
				texts = append(texts, *call.Summary)
			}
		}
	}
	return texts
}

var numberTokenPattern = regexp.MustCompile(`\d+[a-zA-Zа-яёА-ЯЁ]?`)

func extractNumberTokens(text string) []string {
	return numberTokenPattern.FindAllString(text, -1)
}

func containsToken(tokens []string, want string) bool {
	want = strings.ToLower(want)
	for _, t := range tokens {
		if strings.ToLower(t) == want {
			return true
		}
	}
	return false
}
