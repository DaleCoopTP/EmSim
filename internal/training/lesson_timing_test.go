package training

import (
	"errors"
	"testing"

	"emsim/internal/auth"
)

func TestValidateTimingBounds(t *testing.T) {
	spawn := func(v int) *int { return &v }
	for _, tc := range []struct {
		name   string
		timing Timing
		level  auth.Level
		field  string // empty = valid
	}{
		{"defaults", Timing{OpenS: 30, PrimaryS: 30, CompleteS: 180}, auth.LevelEasy, ""},
		{"lower bounds", Timing{OpenS: 10, PrimaryS: 10, CompleteS: 60}, auth.LevelEasy, ""},
		{"upper bounds", Timing{OpenS: 300, PrimaryS: 600, CompleteS: 3600}, auth.LevelEasy, ""},
		{"open too short", Timing{OpenS: 9, PrimaryS: 30, CompleteS: 180}, auth.LevelEasy, "timing.open_s"},
		{"open too long", Timing{OpenS: 301, PrimaryS: 400, CompleteS: 180}, auth.LevelEasy, "timing.open_s"},
		{"primary before open", Timing{OpenS: 40, PrimaryS: 30, CompleteS: 180}, auth.LevelEasy, "timing.primary_s"},
		{"primary too long", Timing{OpenS: 30, PrimaryS: 601, CompleteS: 180}, auth.LevelEasy, "timing.primary_s"},
		{"complete too short", Timing{OpenS: 30, PrimaryS: 30, CompleteS: 59}, auth.LevelEasy, "timing.complete_s"},
		{"complete too long", Timing{OpenS: 30, PrimaryS: 30, CompleteS: 3601}, auth.LevelEasy, "timing.complete_s"},
		{"spawn on hard", Timing{OpenS: 30, PrimaryS: 30, CompleteS: 180, SpawnEveryS: spawn(150)}, auth.LevelHard, ""},
		{"spawn on easy", Timing{OpenS: 30, PrimaryS: 30, CompleteS: 180, SpawnEveryS: spawn(150)}, auth.LevelEasy, "timing.spawn_every_s"},
		{"spawn zero", Timing{OpenS: 30, PrimaryS: 30, CompleteS: 180, SpawnEveryS: spawn(0)}, auth.LevelHard, "timing.spawn_every_s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTiming(tc.timing, tc.level)
			if tc.field == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != tc.field {
				t.Fatalf("err=%v want field %q", err, tc.field)
			}
		})
	}
}
