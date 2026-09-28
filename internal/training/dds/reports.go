package dds

import (
	"time"

	"emsim/internal/training"

	"github.com/google/uuid"
)

// Report is one delivered crew/contact message on a DDS item and the
// trainee's actual reaction to it (ADR-031) — the instructor monitor's
// view. It deliberately ignores the scenario's expects: judging the delay
// is the rubric's job (slice ДДС-3), the monitor only shows it.
type Report struct {
	ItemID      uuid.UUID
	EventKey    string
	Delivery    string
	From        string
	FromLabel   string
	DeliveredAt time.Time
	// AnsweredAt is set for an answered phone_incoming only.
	AnsweredAt *time.Time
	// Missed is a phone_incoming left unanswered past its ring window.
	Missed bool
	// ReactionAt is the first applied set_status after the message
	// reached the trainee: its delivery for a notice, its answer for an
	// incoming call; nil while there is none (or the call went unanswered).
	ReactionAt *time.Time
}

// ReportReactions projects item's delivered notice/phone_incoming events.
// item.Calls and item.Contacts must be loaded; actions are the item's log.
func ReportReactions(item training.Item, events []training.DeliveredEvent, actions []training.Action, now time.Time) []Report {
	labels := make(map[string]string, len(item.Contacts))
	for _, c := range item.Contacts {
		labels[c.Key] = c.Label
	}
	answeredAt := map[string]time.Time{}
	for _, c := range item.Calls {
		if !c.Outgoing() && c.EventKey != "" {
			answeredAt[c.EventKey] = c.StartedAt
		}
	}
	var out []Report
	for _, e := range events {
		if e.Delivery != "notice" && e.Delivery != "phone_incoming" {
			continue
		}
		label := labels[e.From]
		if label == "" {
			label = e.From
		}
		r := Report{ItemID: item.ID, EventKey: e.Key, Delivery: e.Delivery, From: e.From, FromLabel: label, DeliveredAt: e.DeliveredAt}
		start := e.DeliveredAt
		if e.Delivery == "phone_incoming" {
			at, ok := answeredAt[e.Key]
			if !ok {
				r.Missed = now.After(RingUntil(training.IncomingRing{EventKey: e.Key, From: e.From, DeliveredAt: e.DeliveredAt}))
				out = append(out, r)
				continue
			}
			r.AnsweredAt = &at
			start = at
		}
		for _, a := range actions {
			if a.Accepted && a.Type == training.CommandSetStatus && !a.ServerAt.Before(start) {
				at := a.ServerAt
				r.ReactionAt = &at
				break
			}
		}
		out = append(out, r)
	}
	return out
}
