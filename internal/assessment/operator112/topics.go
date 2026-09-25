package operator112

import (
	"regexp"

	"emsim/internal/assessment"
	"emsim/internal/content"
	trainingintake "emsim/internal/training/operator112"
)

// compileTopicPattern mirrors internal/content/validate.go's own RE2
// convention for caller-facing patterns (implicit "(?i)" prefix, \b
// forbidden project-wide since RE2's \b is ASCII-only and never fires
// against Cyrillic text) — rubric.operator112.json's own params are
// trusted config, not scenario-author input, so an invalid pattern is a
// packaging bug: it is simply skipped (never matches) rather than
// panicking a worker mid-evaluation.
func compileTopicPattern(pattern string) *regexp.Regexp {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil
	}
	return re
}

func textMatchesAny(text string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re != nil && re.MatchString(text) {
			return true
		}
	}
	return false
}

// callerTopicsRule is CALLER_TOPICS (ADR-026 §2.3): each rubric topic's
// own keyword/RE2 dictionary is matched against the operator's own
// transcript lines (Speaker=="operator" — the trainee's questions, not
// the applicant's answers). No reference is needed; this rule always
// runs when there was a conversation at all.
//
// card_only (no dialogue) makes the whole criterion not_applicable. For
// a prepared (scripted) dialogue, a topic none of the scenario's own
// dialogue.questions[].text could ever match is not_applicable within
// the block (interpretation §10.6 — a prepared scenario that never
// offers a question about, say, on-site access cannot fault the trainee
// for not asking about it) and the block's weight renormalizes over the
// remaining topics, the same "N/A within block" mechanism ADDRESS_
// FIELDS already uses. free_text has no fixed question list to check
// against, so every topic stays reachable there.
func callerTopicsRule(ev trainingintake.EvidenceBody, intake *content.Intake112, c assessment.RubricCriterion) assessment.CriterionResult {
	noDialogueExplanation := paramString(c.Params, "no_dialogue_explanation", "разговора с заявителем не было")
	if intake.Mode == "card_only" || intake.Dialogue == nil {
		return assessment.CriterionResult{ID: c.ID, Status: assessment.CriterionNotApplicable, Weight: c.Weight, Critical: c.Critical, Explanation: noDialogueExplanation}
	}

	var topics []topicParam
	_ = decodeParam(c.Params, "topics", &topics)
	prepared := intake.CallerMode != content.CallerModeFreeText

	operatorLines := make([]trainingLine, 0, len(ev.IntakeState.Transcript))
	for _, line := range ev.IntakeState.Transcript {
		if line.Speaker == "operator" {
			operatorLines = append(operatorLines, trainingLine{ID: line.ID, Text: line.Text})
		}
	}

	var totalPoints, earnedPoints float64
	var evRefs []string
	details := make([]assessment.CriterionDetail, 0, len(topics))
	for _, topic := range topics {
		patterns := make([]*regexp.Regexp, 0, len(topic.Patterns))
		for _, p := range topic.Patterns {
			patterns = append(patterns, compileTopicPattern(p))
		}
		if prepared && !questionsCoverTopic(intake.Dialogue.Questions, patterns) {
			details = append(details, assessment.CriterionDetail{Key: topic.ID, Label: topic.Label, MaxPoints: topic.Points, Status: assessment.CriterionNotApplicable})
			continue
		}
		totalPoints += topic.Points
		touched := false
		for _, line := range operatorLines {
			if textMatchesAny(line.Text, patterns) {
				touched = true
				evRefs = append(evRefs, "line:"+line.ID)
			}
		}
		points, status := 0.0, assessment.CriterionNotMet
		if touched {
			points, status = topic.Points, assessment.CriterionMet
		}
		earnedPoints += points
		details = append(details, assessment.CriterionDetail{Key: topic.ID, Label: topic.Label, Points: points, MaxPoints: topic.Points, Status: status})
	}

	if totalPoints == 0 {
		return assessment.CriterionResult{
			ID: c.ID, Status: assessment.CriterionNotApplicable, Weight: c.Weight, Critical: c.Critical,
			Explanation: "ни одна тема недостижима в этом сценарии", Details: details,
		}
	}
	score := earnedPoints / totalPoints
	return assessment.CriterionResult{
		ID: c.ID, Status: statusFromScore(score), Score: &score, Weight: c.Weight, Critical: c.Critical,
		EvidenceRefs: evRefs, Details: details,
	}
}

// trainingLine is callerTopicsRule's own minimal projection of an
// IntakeLine — just enough to match text and cite an evidence_ref,
// without importing internal/training here purely for one field pair.
type trainingLine struct {
	ID   string
	Text string
}

// questionsCoverTopic reports whether any of the scenario's own
// dialogue.questions[].text would trigger this topic's own patterns —
// used only for a prepared dialogue's own reachability check.
func questionsCoverTopic(questions []content.Intake112Question, patterns []*regexp.Regexp) bool {
	for _, q := range questions {
		if textMatchesAny(q.Text, patterns) {
			return true
		}
	}
	return false
}
