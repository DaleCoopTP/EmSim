package reporting

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"strconv"
	"time"
)

// Usage statistics for the administrator (ADR-038): how much the system is
// used, per UTC day. Anonymous by construction — counters only, no names,
// no lesson or card content. Preview runs of the scenario editor are not
// lessons and are excluded.

// MaxUsagePeriodDays bounds one request so a day series stays small.
const MaxUsagePeriodDays = 366

var ErrUsagePeriod = errors.New("reporting: invalid usage period")

type UsageDay struct {
	Day               string `json:"day"` // YYYY-MM-DD, UTC
	Logins            int    `json:"logins"`
	ActiveUsers       int    `json:"active_users"`
	LessonsStarted    int    `json:"lessons_started"`
	LessonsStopped    int    `json:"lessons_stopped"`
	CardsClosed       int    `json:"cards_closed"`
	AutoAssessments   int    `json:"auto_assessments"`
	ExpertAssessments int    `json:"expert_assessments"`
	CallerReplies     int    `json:"caller_replies"`
	JudgeCalls        int    `json:"judge_calls"`
}

type UsageByRole struct {
	Role        string `json:"role"`
	Logins      int    `json:"logins"`
	ActiveUsers int    `json:"active_users"`
}

type UsageByExercise struct {
	ExerciseType   string `json:"exercise_type"`
	LessonsStarted int    `json:"lessons_started"`
	LessonsStopped int    `json:"lessons_stopped"`
	CardsClosed    int    `json:"cards_closed"`
}

// Usage is the whole answer: the day series and the period totals.
// Totals.ActiveUsers counts distinct users over the period, not the sum of
// the days.
type Usage struct {
	From       time.Time         `json:"from"`
	To         time.Time         `json:"to"`
	Days       []UsageDay        `json:"days"`
	Totals     UsageDay          `json:"totals"`
	ByRole     []UsageByRole     `json:"by_role"`
	ByExercise []UsageByExercise `json:"by_exercise"`
}

type UsageStore interface {
	Usage(ctx context.Context, from, to time.Time) (Usage, error)
}

// NormalizeUsagePeriod snaps the period to whole UTC days: from is the
// start of its day, to the start of the day after the requested end
// (exclusive), and the span is checked.
func NormalizeUsagePeriod(from, to time.Time) (time.Time, time.Time, error) {
	from = from.UTC().Truncate(24 * time.Hour)
	to = to.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	if !to.After(from) || to.Sub(from) > MaxUsagePeriodDays*24*time.Hour {
		return from, to, ErrUsagePeriod
	}
	return from, to, nil
}

// UsageCSV writes the day series, then one totals row.
func UsageCSV(u Usage) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write([]byte{0xef, 0xbb, 0xbf})
	cw := csv.NewWriter(&buf)
	cw.Comma = ';'
	cw.UseCRLF = true
	_ = cw.Write([]string{"day", "logins", "active_users", "lessons_started", "lessons_stopped", "cards_closed", "auto_assessments", "expert_assessments", "caller_replies", "judge_calls"})
	row := func(d UsageDay) []string {
		return []string{d.Day, strconv.Itoa(d.Logins), strconv.Itoa(d.ActiveUsers), strconv.Itoa(d.LessonsStarted), strconv.Itoa(d.LessonsStopped), strconv.Itoa(d.CardsClosed), strconv.Itoa(d.AutoAssessments), strconv.Itoa(d.ExpertAssessments), strconv.Itoa(d.CallerReplies), strconv.Itoa(d.JudgeCalls)}
	}
	for _, d := range u.Days {
		_ = cw.Write(row(d))
	}
	t := u.Totals
	t.Day = "total"
	_ = cw.Write(row(t))
	cw.Flush()
	return buf.Bytes(), cw.Error()
}
