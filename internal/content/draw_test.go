package content

import (
	"reflect"
	"testing"
)

func drawVersion(typeCode, typeName string, difficulty int, spawn bool) ScenarioVersionRecord {
	var body Body
	body.Card.Incident.TypeCode = typeCode
	body.Card.Incident.TypeName = typeName
	if spawn {
		body.Events = []Event{{Key: "e1", Delivery: "spawn_card"}}
	}
	return ScenarioVersionRecord{Difficulty: difficulty, Body: body}
}

func TestCategoryOfIsTheClassifierSection(t *testing.T) {
	for code, want := range map[string]string{"14080106": "14", "22020000": "22", "1": "", "": ""} {
		var body Body
		body.Card.Incident.TypeCode = code
		if got := CategoryOf(body); got != want {
			t.Errorf("CategoryOf(%q)=%q want %q", code, got, want)
		}
	}
}

func TestLevelDifficultyBandsCoverOneToTenWithoutOverlap(t *testing.T) {
	seen := map[int]string{}
	for _, level := range []string{"easy", "medium", "hard"} {
		lo, hi, ok := LevelDifficultyBand(level)
		if !ok {
			t.Fatalf("no band for %s", level)
		}
		for d := lo; d <= hi; d++ {
			if prev, dup := seen[d]; dup {
				t.Fatalf("difficulty %d in both %s and %s", d, prev, level)
			}
			seen[d] = level
			if got, _ := LevelOfDifficulty(d); got != level {
				t.Fatalf("LevelOfDifficulty(%d)=%q want %q", d, got, level)
			}
		}
	}
	if len(seen) != 10 {
		t.Fatalf("bands cover %d difficulties, want 10", len(seen))
	}
	if _, _, ok := LevelDifficultyBand("expert"); ok {
		t.Fatal("unknown level must have no band")
	}
	if _, ok := LevelOfDifficulty(0); ok {
		t.Fatal("difficulty 0 has no level")
	}
	if _, ok := LevelOfDifficulty(11); ok {
		t.Fatal("difficulty 11 has no level")
	}
}

func TestSummarizeCategories(t *testing.T) {
	got := SummarizeCategories([]ScenarioVersionRecord{
		drawVersion("14080106", "Дерево упало во дворе", 2, false),
		drawVersion("14080106", "Дерево упало во дворе", 1, false),
		drawVersion("14020300", "Течь (прорыв трубы)", 4, false),
		drawVersion("22020000", "Без сознания", 3, false),
		drawVersion("14080106", "Дерево со spawn_card", 2, true), // never drawn
		drawVersion("9", "Слишком короткий код", 2, false),       // no section
	})
	want := []CategorySummary{
		{Code: "14", TypeNames: []string{"Дерево упало во дворе", "Течь (прорыв трубы)"}, CountByLevel: LevelCounts{Easy: 2, Medium: 1}},
		{Code: "22", TypeNames: []string{"Без сознания"}, CountByLevel: LevelCounts{Easy: 1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}
