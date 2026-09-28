package training

import (
	"context"
	"errors"

	"emsim/internal/auth"
	"emsim/internal/content"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MonitorRow is one assignment's live state — openapi.yaml's Monitor.
// rows[] entry, minus Online (a best-effort in-process presence value
// the HTTP layer, not this pure-database read, is in the best position
// to attach — RFC-001 §7.7: "online — best-effort presence текущего
// API-процесса").
type MonitorRow struct {
	WorkstationNo int
	User          auth.User
	RunID         uuid.UUID
	// ActiveItems is every non-terminal item of the run, ordinal
	// ascending (ItemsByRun's own order) — a hard-level run may have
	// more than one (ADR-018: "Монитор возвращает active_items[], а не
	// один current_item").
	ActiveItems []Item
	QueueLeft   int
	Done        int
	// LastAction is nil until the trainee's run has at least one action
	// across any of its items.
	LastAction *Action
	// Comms (ADR-031, DDS only) is, per active item, what the monitor's
	// crew-report view is computed from.
	Comms []ItemComms
}

// ItemComms is one active DDS item's communication record for the
// monitor: the item with Calls and Contacts loaded, its delivered events
// and its action log. The HTTP layer derives the report view from it
// (dds.ReportReactions) — training itself never imports exercise rules.
type ItemComms struct {
	Item    Item
	Events  []DeliveredEvent
	Actions []Action
}

// MonitorResult is Service.Monitor's read — everything openapi.yaml's
// Monitor needs except last_event_id/server_time (the HTTP layer's own
// concerns: a cursor from the realtime Hub, "now" from a source that
// does not need its own transaction).
type MonitorResult struct {
	Lesson Lesson
	Rows   []MonitorRow
}

// Monitor reads the instructor's live view of one lesson straight from
// PostgreSQL (RFC-001 §7.7: "Monitor snapshot читать из PostgreSQL") —
// it is never derived from the SSE buffer, which is only ever a hint to
// re-read this. An assignment with no run yet (the lesson is still
// draft) is omitted rather than emitted with a zero run_id, since
// openapi.yaml's Monitor.rows[].run_id is required.
func (s *Service) Monitor(ctx context.Context, actor auth.Principal, lessonID uuid.UUID) (MonitorResult, error) {
	var result MonitorResult
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lesson, err := s.store.LessonByID(ctx, tx, lessonID, LockNone)
		if err != nil {
			return err
		}
		if lesson.InstructorID != actor.UserID {
			return ErrNotFound
		}
		result.Lesson = lesson

		assignments, err := s.store.AssignmentsByLesson(ctx, tx, lessonID)
		if err != nil {
			return err
		}
		runs, err := s.store.RunsByLesson(ctx, tx, lessonID)
		if err != nil {
			return err
		}
		runByUser := make(map[uuid.UUID]Run, len(runs))
		for _, run := range runs {
			runByUser[run.UserID] = run
		}

		for _, a := range assignments {
			run, ok := runByUser[a.UserID]
			if !ok {
				continue
			}
			user, err := s.users.UserByID(ctx, tx, a.UserID)
			if err != nil {
				return err
			}
			items, err := s.store.ItemsByRun(ctx, tx, run.ID)
			if err != nil {
				return err
			}
			row := MonitorRow{WorkstationNo: a.WorkstationNo, User: user, RunID: run.ID}
			for _, it := range items {
				if it.State == ItemClosed || it.State == ItemInterrupted {
					row.Done++
				} else {
					row.ActiveItems = append(row.ActiveItems, it)
				}
			}
			if lesson.ExerciseType == content.ExerciseTypeDDSProcessing {
				for _, it := range row.ActiveItems {
					comms, err := s.itemComms(ctx, tx, it)
					if err != nil {
						return err
					}
					row.Comms = append(row.Comms, comms)
				}
			}
			row.QueueLeft = len(a.ScenarioVersionIDs) - run.QueueCursor
			if row.QueueLeft < 0 {
				row.QueueLeft = 0
			}
			lastAction, err := s.store.LastActionByRun(ctx, tx, run.ID)
			switch {
			case err == nil:
				row.LastAction = &lastAction
			case errors.Is(err, ErrNotFound):
				// No action yet — a freshly offered item nobody opened.
			default:
				return err
			}
			result.Rows = append(result.Rows, row)
		}
		return nil
	})
	if err != nil {
		return MonitorResult{}, err
	}
	return result, nil
}

func (s *Service) itemComms(ctx context.Context, tx pgx.Tx, item Item) (ItemComms, error) {
	var err error
	if item.Calls, err = s.store.CallsByItem(ctx, tx, item.ID); err != nil {
		return ItemComms{}, err
	}
	version, err := s.scenarios.VersionByID(ctx, tx, item.ScenarioVersionID)
	if err != nil {
		return ItemComms{}, err
	}
	item.Contacts = version.Body.Contacts
	events, err := s.deliveredEventsForItem(ctx, tx, item.ID, item.ScenarioVersionID)
	if err != nil {
		return ItemComms{}, err
	}
	actions, err := s.store.ActionsByItem(ctx, tx, item.ID)
	if err != nil {
		return ItemComms{}, err
	}
	return ItemComms{Item: item, Events: events, Actions: actions}, nil
}
