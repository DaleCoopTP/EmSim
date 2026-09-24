package operator112

import (
	"context"
	"time"
)

// StubCallerReplierAdapter is what StubCallerReplier reports as its own
// IntakeCallerTurn.Adapter — visible to the instructor's review so a
// stub-generated reply is never mistaken for a future model's, without
// a separate server-side setting (ADR-024).
const StubCallerReplierAdapter = "stub/v1"

// stubCallerPhrases are 112-5a's six fixed replies. They deliberately do
// not depend on any scenario's own facts or CallerReplyRequest.Turn's
// caller — StubCallerReplier is a technical placeholder exercising the
// asynchronous caller.reply protocol (ADR-024), not a learning case; the
// text itself carries no scenario-specific meaning
// (slice-112-5a-plan.md).
var stubCallerPhrases = []string{
	"Я упал... Глаз очень болит, я его открыть не могу. Помогите, пожалуйста!",
	"Москва, улица Космонавтов, дом 1, корпус 4.",
	"На спортивной площадке возле дома, на улице",
	"Произошло примерно четыре минуты назад.",
	"Кудрявцев Алексей Иванович.\nМой телефон: +7 900 000-00-00.",
	"Хорошо, остаюсь на связи. Жду помощи.",
}

// StubCallerReplier is 112-5a's own CallerReplier (ADR-024): the Nth
// message from the operator gets the Nth fixed phrase, and every
// message past the sixth repeats the last one. Delay simulates a
// model's own response latency, so the whole asynchronous protocol
// (pending state, cancellation on hold/end/mark_call_dropped, stop's
// barrier, attempt exhaustion via the registered Finalizer) is
// exercised in 112-5a exactly as it will be once 112-5b swaps this
// struct for a real model adapter — nothing else about the protocol
// changes. A zero Delay answers immediately (e2e tests use this to stay
// fast); ctx cancellation (the worker's own lease-bound timeout) is
// honored either way.
type StubCallerReplier struct {
	Delay time.Duration
}

func (StubCallerReplier) Adapter() string { return StubCallerReplierAdapter }

func (r StubCallerReplier) Reply(ctx context.Context, req CallerReplyRequest) (CallerReply, error) {
	if r.Delay > 0 {
		timer := time.NewTimer(r.Delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return CallerReply{}, ctx.Err()
		}
	}
	index := req.Turn - 1
	if index < 0 {
		index = 0
	}
	if index >= len(stubCallerPhrases) {
		index = len(stubCallerPhrases) - 1
	}
	return CallerReply{Text: stubCallerPhrases[index]}, nil
}
