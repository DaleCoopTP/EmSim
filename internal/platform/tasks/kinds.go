// Package tasks is the platform task queue: the durable, polymorphic-scope
// job table workers claim from, and the failure-recovery protocol around it.
// It is ported by copy from orchestration-core's internal/queue,
// internal/recovery, and internal/worker packages, which this port folds
// into one package (ADR-010: "очередь из orchestration-core переносится
// копированием"). New in this port, not from core: kind registry (kinds.go),
// scope-polymorphic Lease, waiting/cancelled statuses, and Cancel.
package tasks

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrInvalidSpec   = errors.New("invalid task kind specification")
	ErrDuplicateKind = errors.New("task kind is already registered")
	ErrUnknownKind   = errors.New("task kind is not registered")
)

// Spec is the per-kind configuration a worker needs to run a task kind:
// which pool claims it, its attempt budget, lease duration, and retry base.
// docs/architecture/02-core-reuse.md's execution contract and
// design-docs/contracts/tasks.schema.json list priorities per kind family
// (evaluate=100, generation=50, advice=10); Priority here is that value.
type Spec struct {
	Name        Kind
	Pool        string
	MaxAttempts int
	Lease       time.Duration
	RetryBase   time.Duration
	Priority    int16
}

func (s Spec) validate() error {
	if !s.Name.Valid() || s.Pool == "" || s.MaxAttempts < 1 || s.RetryBase <= 0 || s.Priority < 0 {
		return ErrInvalidSpec
	}
	return nil
}

// Registry is the single place task kinds are declared (risk mitigation
// from docs/technical-discovery.md §6: "Реестр kind'ов в одном месте"). It
// is constructed against a Policy so Register can reject a lease too short
// for that policy's heartbeat budget instead of failing silently at
// runtime.
type Registry struct {
	policy Policy
	mu     sync.RWMutex
	specs  map[Kind]Spec
}

func NewRegistry(policy Policy) (*Registry, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &Registry{policy: policy, specs: make(map[Kind]Spec)}, nil
}

// Register adds a task kind. It fails if the kind is already registered,
// the spec is malformed, or the lease is not comfortably longer than the
// registry's heartbeat budget (heartbeat interval + jitter + safety
// margin) — the same guarantee core's Policy.Validate() enforced for its
// single TaskLease field, now checked per kind.
func (r *Registry) Register(spec Spec) error {
	if err := spec.validate(); err != nil {
		return err
	}
	if spec.Lease <= r.policy.minimumLease() {
		return ErrInvalidSpec
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.specs[spec.Name]; exists {
		return ErrDuplicateKind
	}
	r.specs[spec.Name] = spec
	return nil
}

func (r *Registry) Lookup(kind Kind) (Spec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.specs[kind]
	return spec, ok
}

// Pool returns the kinds registered for a given pool name, sorted for
// deterministic SQL claim clauses.
func (r *Registry) Pool(name string) []Kind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var kinds []Kind
	for kind, spec := range r.specs {
		if spec.Pool == name {
			kinds = append(kinds, kind)
		}
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}
