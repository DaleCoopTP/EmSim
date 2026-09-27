package dds

import (
	"time"

	"emsim/internal/content"
	"emsim/internal/training"
)

// CardStatus is ADR-030's derived card status (памятка ДДС, стр. 27–28),
// never stored: the openapi ItemSummary.card_status enum.
type CardStatus string

const (
	CardRegistered   CardStatus = "registered"
	CardNotNotified  CardStatus = "not_notified"
	CardInProgress   CardStatus = "in_progress"
	CardRefused      CardStatus = "refused"
	CardCompleted    CardStatus = "completed"
	CardNotCompleted CardStatus = "not_completed"
)

// CardStatusOf derives a DDS card's status from its reaction and the
// server time: no primary decision past the primary deadline is «Не
// оповещено»; not_accepted/refused is «Отказ»; completed and 103's
// completed_without_team are «Завершена»; a card interrupted (stop)
// before either is «Не завершено» — the trainer's compressed-time
// stand-in for the real 48-hour rule.
func CardStatusOf(item training.Item, now time.Time) CardStatus {
	switch item.Reaction {
	case content.ReactionCompleted, content.ReactionCompletedWithoutTeam:
		return CardCompleted
	case content.ReactionNotAccepted, content.ReactionRefused:
		return CardRefused
	}
	switch {
	case item.CloseReason != nil && *item.CloseReason == training.ClosePilotCompleted:
		// ADR-017's pilot finish from accepted: the exercise is done.
		return CardCompleted
	case item.State == training.ItemInterrupted || item.State == training.ItemClosed:
		return CardNotCompleted
	}
	switch item.Reaction {
	case content.ReactionAdded, content.ReactionReceived:
		if now.After(item.Deadlines.PrimaryAt) {
			return CardNotNotified
		}
		return CardRegistered
	}
	return CardInProgress
}
