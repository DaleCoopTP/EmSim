package operator112

import (
	"fmt"
	"time"

	"emsim/internal/assessment"
	trainingintake "emsim/internal/training/operator112"
)

// linearTimingScore is the shared N..2N linear degradation curve
// (ADR-026 §2.4, interpretation §10's confirmed "linear N..2N, 0
// beyond"): 1.0 at or under the norm, sliding to 0.0 at twice the norm,
// clamped at both ends. norm must be positive.
func linearTimingScore(elapsedS, normS float64) float64 {
	score := (2*normS - elapsedS) / normS
	if score > 1 {
		return 1
	}
	if score < 0 {
		return 0
	}
	return score
}

// timingResult builds a T_ANSWER/T_FILL CriterionResult from an already
// computed 0..1 score, or not_applicable when the milestone this timing
// covers was never reached at all (nil elapsed) — ADR-026 keeps that
// case distinct from a "no reference" 0: there is no trainee outcome to
// measure yet, not merely nothing to compare it against.
func timingResult(c assessment.RubricCriterion, elapsed *time.Duration, normS float64, evRefs []string, naExplanation string) assessment.CriterionResult {
	if elapsed == nil {
		return assessment.CriterionResult{ID: c.ID, Status: assessment.CriterionNotApplicable, Weight: c.Weight, Critical: c.Critical, Explanation: naExplanation}
	}
	elapsedS := elapsed.Seconds()
	score := linearTimingScore(elapsedS, normS)
	return assessment.CriterionResult{
		ID: c.ID, Status: statusFromScore(score), Score: &score, Weight: c.Weight, Critical: c.Critical,
		EvidenceRefs: evRefs, Explanation: fmt.Sprintf("%.0f с (норматив %.0f с)", elapsedS, normS),
	}
}

// answerTimingRule is T_ANSWER (ADR-026 §2.4): OfferedAt -> the moment
// the trainee actually answered the call (intake_state.answered_at —
// distinct from opened_at, which only marks the card being opened, not
// the call being picked up). A server-restart interruption or a call
// never answered before close both make this not_applicable, never a
// 0 — the trainee had no fair chance to hit the norm.
func answerTimingRule(ev trainingintake.EvidenceBody, c assessment.RubricCriterion) assessment.CriterionResult {
	normS := paramFloat(c.Params, "norm_s", 15)
	if len(ev.Interruptions) > 0 {
		return assessment.CriterionResult{ID: c.ID, Status: assessment.CriterionNotApplicable, Weight: c.Weight, Critical: c.Critical, Explanation: "занятие было прервано"}
	}
	if ev.IntakeState.AnsweredAt == nil {
		return timingResult(c, nil, normS, nil, "вызов не был принят до закрытия карточки")
	}
	elapsed := ev.IntakeState.AnsweredAt.Sub(ev.OfferedAt)
	return timingResult(c, &elapsed, normS, evidenceRefs(ev), "")
}

// fillTimingRule is T_FILL (ADR-026 §2.4): answered_at -> the
// notification's own notified_at (the "оповестить и сохранить" moment —
// ADR-026's own "что проверяется" anchor, consistent with scoredCard),
// minus the trainee's total wait for the AI caller's own replies
// (Σ IntakeCallerTurn resolved-turn duration, 112-5b — a trainee should
// never be penalized for model latency, RFC-001's own "техническая
// задержка не считается ошибкой обучаемого" applied here). Not reaching
// notify_services at all (stop, or notify never happened) makes this
// not_applicable, the same "milestone not reached" rule P_SERVICES
// already follows.
func fillTimingRule(ev trainingintake.EvidenceBody, c assessment.RubricCriterion) assessment.CriterionResult {
	normS := paramFloat(c.Params, "norm_s", 90)
	if len(ev.Interruptions) > 0 {
		return assessment.CriterionResult{ID: c.ID, Status: assessment.CriterionNotApplicable, Weight: c.Weight, Critical: c.Critical, Explanation: "занятие было прервано"}
	}
	if ev.IntakeState.AnsweredAt == nil {
		return timingResult(c, nil, normS, nil, "вызов не был принят до закрытия карточки")
	}
	if ev.Notification == nil {
		return timingResult(c, nil, normS, nil, "карточка не была отправлена службам")
	}
	elapsed := ev.Notification.NotifiedAt.Sub(*ev.IntakeState.AnsweredAt) - callerTurnsWait(ev)
	if elapsed < 0 {
		elapsed = 0
	}
	return timingResult(c, &elapsed, normS, evidenceRefs(ev), "")
}

// callerTurnsWait sums every IntakeCallerTurn's own resolved duration
// (answered, cancelled, or failed all set resolved_at — see
// internal/training.Service.ApplyCallerReply/FailCallerTurn/
// cancelPendingCallerTurn) — the trainee could not have been filling the
// card any faster while a chat message's reply was still outstanding.
func callerTurnsWait(ev trainingintake.EvidenceBody) time.Duration {
	var total time.Duration
	for _, turn := range ev.IntakeState.CallerTurns {
		if turn.ResolvedAt != nil {
			total += turn.ResolvedAt.Sub(turn.RequestedAt)
		}
	}
	return total
}
