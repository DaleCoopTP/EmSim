package content

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
)

// ДДС-6/ADR-035: scenario categories and difficulty bands for the random
// queue fill. A category is the classifier section — the first two
// characters of card.incident.type_code (14 — housing, 22 — medical, ...);
// until the real classifier arrives (ДДС-5) only the section code is known.

// CategoryOf is a scenario body's category, "" when its type_code is
// shorter than a section code.
func CategoryOf(body Body) string {
	code := []rune(body.Card.Incident.TypeCode)
	if len(code) < 2 {
		return ""
	}
	return string(code[:2])
}

// DrawableWithoutSpawn reports whether a scenario can be drawn into a
// random queue: one with a spawn_card event has its own fixed queue order
// (training.checkSpawnQueuePlan), so it is never drawn.
func DrawableWithoutSpawn(body Body) bool {
	for _, e := range body.Events {
		if e.Delivery == "spawn_card" {
			return false
		}
	}
	return true
}

// LevelDifficultyBand is the difficulty range (1–10, inclusive) a lesson
// level draws from: easy 1–3, medium 4–6, hard 7–10. ok is false for an
// unknown level.
func LevelDifficultyBand(level string) (min, max int, ok bool) {
	switch level {
	case "easy":
		return 1, 3, true
	case "medium":
		return 4, 6, true
	case "hard":
		return 7, 10, true
	}
	return 0, 0, false
}

// LevelOfDifficulty is LevelDifficultyBand's inverse; a difficulty outside
// 1–10 has no level.
func LevelOfDifficulty(difficulty int) (string, bool) {
	for _, level := range []string{"easy", "medium", "hard"} {
		if lo, hi, _ := LevelDifficultyBand(level); difficulty >= lo && difficulty <= hi {
			return level, true
		}
	}
	return "", false
}

// LevelCounts is a per-level scenario count.
type LevelCounts struct {
	Easy, Medium, Hard int
}

// CategorySummary is one row of GET /scenarios/categories.
type CategorySummary struct {
	Code         string
	TypeNames    []string
	CountByLevel LevelCounts
}

// SummarizeCategories groups drawable scenario versions by category, with
// their distinct incident type names and how many fall in each level's
// difficulty band. Rows are ordered by code, names alphabetically.
func SummarizeCategories(versions []ScenarioVersionRecord) []CategorySummary {
	byCode := map[string]*CategorySummary{}
	names := map[string]map[string]bool{}
	for _, v := range versions {
		if !DrawableWithoutSpawn(v.Body) {
			continue
		}
		code := CategoryOf(v.Body)
		if code == "" {
			continue
		}
		row := byCode[code]
		if row == nil {
			row = &CategorySummary{Code: code}
			byCode[code] = row
			names[code] = map[string]bool{}
		}
		names[code][v.Body.Card.Incident.TypeName] = true
		switch level, _ := LevelOfDifficulty(v.Difficulty); level {
		case "easy":
			row.CountByLevel.Easy++
		case "medium":
			row.CountByLevel.Medium++
		case "hard":
			row.CountByLevel.Hard++
		}
	}
	out := make([]CategorySummary, 0, len(byCode))
	for code, row := range byCode {
		for name := range names[code] {
			row.TypeNames = append(row.TypeNames, name)
		}
		sort.Strings(row.TypeNames)
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// ScenarioCategories is GET /scenarios/categories: the classifier sections
// of the approved, non-archived dds_processing scenarios (of one target
// service when given) with per-level counts.
func (s *Service) ScenarioCategories(ctx context.Context, targetService string) ([]CategorySummary, error) {
	var versions []ScenarioVersionRecord
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		versions, err = s.store.ListApprovedDDSVersions(ctx, tx, targetService)
		return err
	})
	if err != nil {
		return nil, err
	}
	return SummarizeCategories(versions), nil
}
