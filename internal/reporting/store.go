package reporting

import (
	"context"
	"emsim/internal/content"

	"github.com/google/uuid"
)

type Store interface {
	LessonReport(context.Context, uuid.UUID) (LessonReport, error)
	Results(context.Context, uuid.UUID) ([]ItemResult, error)
	Progress(context.Context, uuid.UUID) (Progress, error)
	ResultsFor(context.Context, uuid.UUID, content.ExerciseType) ([]ItemResult, error)
	ProgressFor(context.Context, uuid.UUID, content.ExerciseType) (Progress, error)
}
