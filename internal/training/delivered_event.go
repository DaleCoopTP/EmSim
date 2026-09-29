package training

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type DeliveredEvent struct {
	Key         string
	Delivery    string
	From        string
	Text        string
	Voice       bool
	DeliveredAt time.Time
	Late        bool
}

// IncomingRing is one delivered phone_incoming event (ADR-031): the call
// a DDS trainee may answer while it still rings.
type IncomingRing struct {
	EventKey    string
	From        string
	DeliveredAt time.Time
}

// IncomingRings picks the delivered phone_incoming events out of events.
func IncomingRings(events []DeliveredEvent) []IncomingRing {
	var out []IncomingRing
	for _, e := range events {
		if e.Delivery == "phone_incoming" {
			out = append(out, IncomingRing{EventKey: e.Key, From: e.From, DeliveredAt: e.DeliveredAt})
		}
	}
	return out
}

// deliveredEventsForItem loads itemID's own delivered events (scheduled/
// skipped ones are never shown to the trainee — a scheduled event would
// leak the scenario's future timeline) and resolves each against its
// scenario version's event definition. A definition that has since gone
// missing (should not happen — scenario versions are immutable) is
// skipped rather than failing the whole read.
func (s *Service) deliveredEventsForItem(ctx context.Context, tx pgx.Tx, itemID, scenarioVersionID uuid.UUID) ([]DeliveredEvent, error) {
	events, err := s.store.ItemEventsByItem(ctx, tx, itemID)
	if err != nil {
		return nil, err
	}
	var delivered []ItemEvent
	for _, e := range events {
		if e.State == EventDelivered {
			delivered = append(delivered, e)
		}
	}
	if len(delivered) == 0 {
		return []DeliveredEvent{}, nil
	}
	version, err := s.scenarios.VersionByID(ctx, tx, scenarioVersionID)
	if err != nil {
		return nil, err
	}
	out := make([]DeliveredEvent, 0, len(delivered))
	for _, e := range delivered {
		definition, ok := eventByKey(version.Body.Events, e.EventKey)
		if !ok {
			continue
		}
		deliveredAt := e.DeliveredAt
		if deliveredAt == nil {
			continue
		}
		out = append(out, DeliveredEvent{
			Key: e.EventKey, Delivery: definition.Delivery, From: definition.From, Text: definition.Text,
			Voice: definition.Voice, DeliveredAt: *deliveredAt, Late: e.Late,
		})
	}
	return out, nil
}
