package training

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DeliveredEvent is one delivered scenario event's trainee-facing view —
// openapi.yaml's DeliveredEvent. It combines an item_events row (what
// happened, and when) with the scenario version's own event definition
// (delivery/from/text — the content a trainee is meant to see), which
// only the application service can resolve (it alone holds both the
// training.Store and the content.ScenarioReader port); a pure Exercise
// never touches content directly (CLAUDE.md's module boundary), and
// evidence.go's EvidenceEvent stays the narrower state-only projection
// evidence.schema.json actually requires. expects is never carried here
// — RFC-001 §7.2: "expects обучаемому не раскрывать".
type DeliveredEvent struct {
	Key         string
	Delivery    string
	From        string
	Text        string
	Voice       bool
	DeliveredAt time.Time
	Late        bool
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
