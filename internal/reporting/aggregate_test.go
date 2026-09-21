package reporting

import (
	"testing"

	"github.com/google/uuid"
)

func TestEnrichDoesNotTurnMissingScoreIntoZero(t *testing.T) {
	user := uuid.New()
	score := 90.0
	participants, aggregates := Enrich([]ReportItem{{ItemResult: ItemResult{ItemID: uuid.New(), AssessmentStatus: AssessmentReady, Score: &score, Errors: []PublicError{{CriterionID: "D_PRIMARY"}}}, UserID: user, FullName: "А", WorkstationNo: 1}, {ItemResult: ItemResult{ItemID: uuid.New(), AssessmentStatus: AssessmentNeedsReview}, UserID: user, FullName: "А", WorkstationNo: 1}, {ItemResult: ItemResult{ItemID: uuid.New(), ItemState: "interrupted", AssessmentStatus: AssessmentPending}, UserID: user, FullName: "А", WorkstationNo: 1}})
	if aggregates.AvgScore == nil || *aggregates.AvgScore != 90 || aggregates.PendingAssessments != 2 || aggregates.InterruptedItems != 1 {
		t.Fatalf("aggregates = %+v", aggregates)
	}
	if len(participants) != 1 || participants[0].ReadyAssessments != 1 || participants[0].PendingAssessments != 2 {
		t.Fatalf("participants = %+v", participants)
	}
}
