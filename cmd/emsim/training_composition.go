// newTrainingService composes internal/training.Service the same way in
// both processes that need it: the api process (its own HTTP handlers,
// plus the restart-recovery call and scheduler loop in api.go) and the
// worker process (worker_composition.go's lesson.close handler). One
// function so the two compositions cannot drift apart — in particular so
// a Store passed as taskEnqueuer always resolves training.KindLessonClose
// against a Registry that actually has it registered (registerKinds,
// also shared by both processes).
package main

import (
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	"emsim/internal/training"
	"emsim/internal/training/dds"
	trainingpg "emsim/internal/training/postgres"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newTrainingService(pool *pgxpool.Pool, taskEnqueuer training.TaskEnqueuer) *training.Service {
	authStore := authpg.NewStore(pool)
	contentStore := contentpg.NewStore(pool)
	return training.NewService(
		trainingpg.NewStore(pool), authStore, authStore, contentStore, contentStore, taskEnqueuer,
		map[content.ExerciseType]training.Exercise{content.ExerciseTypeDDSProcessing: dds.Exercise},
	)
}
