package assessment

import (
	"context"
	"encoding/json"
	"errors"

	"emsim/internal/platform/tasks"

	"github.com/jackc/pgx/v5"
)

// RetryTarget is assessment.evaluate's admin-retry guard (ADR-033,
// RFC-001 §7.4): an item that already has an auto or expert assessment is
// never evaluated again; a task that failed before its input was sealed
// goes back to waiting for the coordinator, one with a sealed input back
// to pending.
func (s *Service) RetryTarget(ctx context.Context, tx pgx.Tx, task tasks.RetryCandidate) (tasks.RetryTarget, error) {
	if task.ScopeID == nil {
		return "", tasks.ErrNotRetryable
	}
	var payload evaluatePayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return "", errors.New("assessment: decode evaluate payload")
	}
	if _, found, err := s.store.AutoByItem(ctx, tx, *task.ScopeID); err != nil {
		return "", err
	} else if found {
		return "", tasks.ErrNotRetryable
	}
	if hasExpert, err := s.store.HasExpert(ctx, tx, *task.ScopeID); err != nil {
		return "", err
	} else if hasExpert {
		return "", tasks.ErrNotRetryable
	}
	if payload.InputID == nil {
		return tasks.RetryWaiting, nil
	}
	return tasks.RetryPending, nil
}
