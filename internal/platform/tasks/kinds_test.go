// New tests: core had no kind registry (it hard-coded dialogue|judge).
// Covers the risk flagged in docs/technical-discovery.md §6: "Реестр
// kind'ов в одном месте" needs to actually reject a duplicate, a malformed
// name, and a lease too short for the shared heartbeat budget.
package tasks

import (
	"errors"
	"testing"
	"time"
)

func TestNewRegistryRejectsInvalidPolicy(t *testing.T) {
	if _, err := NewRegistry(Policy{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("invalid policy error = %v", err)
	}
}

func TestRegistryRejectsDuplicateBadNameAndShortLease(t *testing.T) {
	policy := DefaultPolicy()
	registry, err := NewRegistry(policy)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	valid := Spec{Name: "system.noop", Pool: "short", MaxAttempts: 3, Lease: 2 * time.Minute, RetryBase: time.Second, Priority: 10}
	if err := registry.Register(valid); err != nil {
		t.Fatalf("register valid spec: %v", err)
	}
	if err := registry.Register(valid); !errors.Is(err, ErrDuplicateKind) {
		t.Fatalf("duplicate register error = %v", err)
	}

	badName := valid
	badName.Name = "noop"
	if err := registry.Register(badName); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("undotted kind name error = %v", err)
	}

	noPool := valid
	noPool.Name = "system.no_pool"
	noPool.Pool = ""
	if err := registry.Register(noPool); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("empty pool error = %v", err)
	}

	// A lease at or below the heartbeat budget (interval + jitter +
	// safety margin) could expire between two heartbeats.
	shortLease := valid
	shortLease.Name = "system.short_lease"
	shortLease.Lease = policy.minimumLease()
	if err := registry.Register(shortLease); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("lease at heartbeat budget error = %v", err)
	}

	if spec, ok := registry.Lookup("system.noop"); !ok || spec.Pool != "short" {
		t.Fatalf("lookup registered kind = %#v/%t", spec, ok)
	}
	if _, ok := registry.Lookup("system.unknown"); ok {
		t.Fatal("lookup unregistered kind succeeded")
	}
}

func TestRegistryPoolListsSortedKinds(t *testing.T) {
	registry, err := NewRegistry(DefaultPolicy())
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	for _, spec := range []Spec{
		{Name: "voice.render", Pool: "short", MaxAttempts: 3, Lease: time.Minute, RetryBase: time.Second, Priority: 10},
		{Name: "assessment.evaluate", Pool: "short", MaxAttempts: 3, Lease: time.Minute, RetryBase: time.Second, Priority: 100},
		{Name: "scenario.generate", Pool: "llm", MaxAttempts: 3, Lease: 10 * time.Minute, RetryBase: 30 * time.Second, Priority: 50},
	} {
		if err := registry.Register(spec); err != nil {
			t.Fatalf("register %s: %v", spec.Name, err)
		}
	}

	got := registry.Pool("short")
	want := []Kind{"assessment.evaluate", "voice.render"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("pool kinds = %v, want %v", got, want)
	}
	if got := registry.Pool("llm"); len(got) != 1 || got[0] != "scenario.generate" {
		t.Fatalf("llm pool kinds = %v", got)
	}
	if got := registry.Pool("missing"); len(got) != 0 {
		t.Fatalf("missing pool kinds = %v, want empty", got)
	}
}
