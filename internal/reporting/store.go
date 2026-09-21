package reporting

import (
	"context"

	"github.com/google/uuid"
)

type Store interface {
	LessonReport(context.Context, uuid.UUID) (LessonReport, error)
	Results(context.Context, uuid.UUID) ([]ItemResult, error)
	Progress(context.Context, uuid.UUID) (Progress, error)
}
