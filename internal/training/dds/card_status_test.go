package dds

import (
	"testing"
	"time"

	"emsim/internal/content"
	"emsim/internal/training"
)

func TestCardStatusOf(t *testing.T) {
	offered := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	primaryDeadline := offered.Add(30 * time.Second)
	pilotDone := training.ClosePilotCompleted
	cases := []struct {
		name     string
		reaction content.Reaction
		state    training.ItemState
		close    *training.CloseReason
		now      time.Time
		want     CardStatus
	}{
		{"offered within deadline", content.ReactionAdded, training.ItemOffered, nil, offered.Add(10 * time.Second), CardRegistered},
		{"opened past deadline", content.ReactionReceived, training.ItemOpened, nil, primaryDeadline.Add(time.Second), CardNotNotified},
		{"accepted", content.ReactionAccepted, training.ItemInProgress, nil, primaryDeadline.Add(time.Minute), CardInProgress},
		{"arrived", content.ReactionArrived, training.ItemInProgress, nil, primaryDeadline.Add(time.Minute), CardInProgress},
		{"not accepted", content.ReactionNotAccepted, training.ItemInProgress, nil, primaryDeadline, CardRefused},
		{"refused and closed", content.ReactionRefused, training.ItemClosed, nil, primaryDeadline, CardRefused},
		{"completed", content.ReactionCompleted, training.ItemClosed, nil, primaryDeadline, CardCompleted},
		{"ambulance without team", content.ReactionCompletedWithoutTeam, training.ItemClosed, nil, primaryDeadline, CardCompleted},
		{"interrupted while working", content.ReactionWorking, training.ItemInterrupted, nil, primaryDeadline, CardNotCompleted},
		{"interrupted before decision", content.ReactionReceived, training.ItemInterrupted, nil, primaryDeadline, CardNotCompleted},
		{"pilot finish from accepted", content.ReactionAccepted, training.ItemClosed, &pilotDone, primaryDeadline, CardCompleted},
	}
	for _, tc := range cases {
		item := training.Item{Reaction: tc.reaction, State: tc.state, CloseReason: tc.close,
			Deadlines: training.Deadlines{PrimaryAt: primaryDeadline}}
		if got := CardStatusOf(item, tc.now); got != tc.want {
			t.Errorf("%s: CardStatusOf = %s, want %s", tc.name, got, tc.want)
		}
	}
}
