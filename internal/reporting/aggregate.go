package reporting

import (
	"cmp"
	"slices"

	"github.com/google/uuid"
)

func Enrich(items []ReportItem) ([]Participant, Aggregates) {
	participants := map[uuid.UUID]*Participant{}
	var scores []float64
	errors := map[string]int{}
	agg := Aggregates{ScoreHistogram: []HistogramBucket{{Bucket: "0-49"}, {Bucket: "50-69"}, {Bucket: "70-84"}, {Bucket: "85-100"}}, TopErrors: []ErrorFrequency{}}
	for _, item := range items {
		p := participants[item.UserID]
		if p == nil {
			p = &Participant{UserID: item.UserID, FullName: item.FullName, WorkstationNo: item.WorkstationNo, Level: item.Level}
			participants[item.UserID] = p
		}
		p.Items++
		if item.ItemState == "interrupted" {
			p.InterruptedItems++
			agg.InterruptedItems++
		}
		if item.AssessmentStatus == AssessmentReady && item.Score != nil {
			p.ReadyAssessments++
			agg.ReadyAssessments++
			scores = append(scores, *item.Score)
			if *item.Score < 50 {
				agg.ScoreHistogram[0].Count++
			} else if *item.Score < 70 {
				agg.ScoreHistogram[1].Count++
			} else if *item.Score < 85 {
				agg.ScoreHistogram[2].Count++
			} else {
				agg.ScoreHistogram[3].Count++
			}
			for _, e := range item.Errors {
				errors[e.CriterionID]++
			}
		} else if item.AssessmentStatus != AssessmentNotAssessed {
			p.PendingAssessments++
			agg.PendingAssessments++
		}
	}
	agg.AvgScore = Average(scores)
	byUser := make([]Participant, 0, len(participants))
	for _, p := range participants {
		byUser = append(byUser, *p)
	}
	slices.SortFunc(byUser, func(a, b Participant) int { return cmp.Compare(a.FullName, b.FullName) })
	for _, p := range byUser {
		var ps, po, pw, pt []float64
		for _, item := range items {
			if item.UserID == p.UserID {
				if item.AssessmentStatus == AssessmentReady && item.Score != nil {
					ps = append(ps, *item.Score)
				}
				if item.OpenSeconds != nil {
					po = append(po, *item.OpenSeconds)
				}
				if item.WorkSeconds != nil {
					pw = append(pw, *item.WorkSeconds)
				}
				if item.TotalSeconds != nil {
					pt = append(pt, *item.TotalSeconds)
				}
			}
		}
		p.AvgScore, p.AvgOpenSeconds, p.AvgWorkSeconds, p.AvgTotalSeconds = Average(ps), Average(po), Average(pw), Average(pt)
		for i := range byUser {
			if byUser[i].UserID == p.UserID {
				byUser[i] = p
				break
			}
		}
	}
	for id, count := range errors {
		agg.TopErrors = append(agg.TopErrors, ErrorFrequency{CriterionID: id, Count: count})
	}
	slices.SortFunc(agg.TopErrors, func(a, b ErrorFrequency) int {
		if n := cmp.Compare(b.Count, a.Count); n != 0 {
			return n
		}
		return cmp.Compare(a.CriterionID, b.CriterionID)
	})
	return byUser, agg
}

func Average(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	var sum float64
	for _, value := range values {
		sum += value
	}
	result := sum / float64(len(values))
	return &result
}
