// Finalizer is new in this port (not from orchestration-core): the
// primitive RFC-001 §7.4/§8 and ADR-016 A2/ADR-019 call for so that a
// task kind whose exhaustion must also produce a domain effect — an
// assessment.evaluate that ran out of attempts still needs its auto
// rev=1 needs_review — can do so atomically with the task's own
// terminal write, without the generic dead_letter/failed path (and its
// task-row-first locking) ever running for that kind. RFC-001 §8 fixes
// the lock order as trainee_assessment_state -> items -> tasks; a
// Finalizer is exactly the escape hatch that lets a kind acquire its
// own domain locks before the task row, instead of the tasks-row-first
// order every other kind's generic terminal write uses.
package tasks

import (
	"context"

	"github.com/google/uuid"
)

// Finalizer performs one task kind's "retries exhausted" domain effect.
// The caller (Recovery) never holds the task's own row lock when it
// calls this — FinalizeExpired manages its own transaction, acquiring
// whatever domain locks its kind needs first, then the task row itself,
// then re-verifying under that fresh lock that the lease still matches
// workerID/token before writing anything. A lease that no longer
// matches — already finalized by a concurrent attempt, or superseded by
// an expert revision in the meantime — is reported as ErrLeaseLost, not
// a storage error; the caller treats that exactly like any other benign
// lost-race outcome.
type Finalizer interface {
	FinalizeExpired(ctx context.Context, taskID uuid.UUID, workerID string, token uint64, terminalStatus TaskStatus, code ErrorCode) error
}

// RegisterFinalizer opts kind out of Recovery's generic dead_letter/failed
// write, once its retry budget is exhausted, in favor of f's own
// domain-first transaction. At most one Finalizer per kind. Call this
// before the Recovery is used to resolve or reap anything of that kind —
// typically once, during worker composition (cmd/emsim), the same place
// registerKinds registers the kind's Spec.
func (r *Recovery) RegisterFinalizer(kind Kind, f Finalizer) error {
	if !kind.Valid() || f == nil {
		return ErrInvalidSpec
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finalizers == nil {
		r.finalizers = make(map[Kind]Finalizer)
	}
	if _, exists := r.finalizers[kind]; exists {
		return ErrDuplicateKind
	}
	r.finalizers[kind] = f
	return nil
}

func (r *Recovery) finalizerFor(kind Kind) (Finalizer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.finalizers[kind]
	return f, ok
}

// finalizerKindNames lists every kind with a registered Finalizer, for
// ReapExpired's own SQL WHERE-clause split between its fast generic path
// (unchanged from before Finalizer existed) and the finalizer-kind path.
func (r *Recovery) finalizerKindNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Always non-nil: ReapExpired passes this as a $N::text[] parameter,
	// and pgx encodes a nil []string as SQL NULL rather than an empty
	// array — "kind = ANY(NULL)" is NULL, not FALSE, which would make
	// "NOT (kind = ANY($n))" NULL too and silently exclude every row from
	// the fast generic path, the one this method must never affect when
	// no Finalizer is registered at all.
	names := make([]string, 0, len(r.finalizers))
	for k := range r.finalizers {
		names = append(names, string(k))
	}
	return names
}
