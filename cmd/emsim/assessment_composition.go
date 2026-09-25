// newAssessmentService composes internal/assessment.Service the one way
// the worker process needs it — training_composition.go's own newTrainingService
// is the pattern this mirrors: *trainingpg.Store and *contentpg.Store are
// passed directly as assessment's own ItemReader/EvidenceReader/
// ScenarioReader ports, since both already satisfy them structurally
// (ItemByID/EvidenceByItem/VersionByID's signatures match exactly, the
// same convention internal/training/ports.go documents for its own
// consumer-owned interfaces onto auth/content).
package main

import (
	"emsim/internal/assessment"
	assessmentdds "emsim/internal/assessment/dds"
	assessmentpg "emsim/internal/assessment/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	"emsim/internal/platform/tasks"
	trainingpg "emsim/internal/training/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newAssessmentService(pool *pgxpool.Pool, taskStore *tasks.Store) *assessment.Service {
	trainingStore := trainingpg.NewStore(pool)
	contentStore := contentpg.NewStore(pool)
	return assessment.NewService(
		assessmentpg.NewStore(pool), trainingStore, trainingStore, trainingStore, contentStore, taskStore,
		assessment.Registry{content.ExerciseTypeDDSProcessing: assessmentdds.Evaluator},
	)
}
