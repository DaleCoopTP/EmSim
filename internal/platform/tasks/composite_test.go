// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/worker/roles_test.go; adapted for the worker|maintenance|all
// roles (composite.go).
package tasks

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestParseRoleRejectsMissingAndUnknown(t *testing.T) {
	for _, value := range []string{"", "Worker", "dialogue"} {
		if _, err := ParseRole(value); !errors.Is(err, ErrInvalidRole) {
			t.Fatalf("ParseRole(%q) error = %v", value, err)
		}
	}
}

func TestRoleCompositionIsolation(t *testing.T) {
	for _, test := range []struct {
		role Role
		want map[string]int
	}{
		{RoleWorker, map[string]int{"worker": 1}},
		{RoleMaintenance, map[string]int{"maintenance": 1}},
		{RoleAll, map[string]int{"worker": 1, "maintenance": 1}},
	} {
		counts := &roleCounts{values: map[string]int{}}
		components := Components{
			Worker: roleSupervisor{"worker", counts}, Maintenance: roleSupervisor{"maintenance", counts},
		}
		if err := RunRole(context.Background(), test.role, components); err != nil {
			t.Fatalf("RunRole(%s): %v", test.role, err)
		}
		if !sameCounts(counts.values, test.want) {
			t.Fatalf("RunRole(%s) counts = %v, want %v", test.role, counts.values, test.want)
		}
	}
}

func TestCompositeStopsAllChildrenOnCancellation(t *testing.T) {
	started := make(chan string, 2)
	stopped := make(chan string, 2)
	composite, err := Composite(blockingSupervisor{"reaper", started, stopped}, blockingSupervisor{"sampler", started, stopped})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- composite.Run(ctx) }()
	for range 2 {
		<-started
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for range 2 {
		seen[<-stopped] = true
	}
	if !seen["reaper"] || !seen["sampler"] {
		t.Fatalf("stopped = %v", seen)
	}
}

type blockingSupervisor struct {
	name             string
	started, stopped chan string
}

func (s blockingSupervisor) Run(ctx context.Context) error {
	s.started <- s.name
	<-ctx.Done()
	s.stopped <- s.name
	return nil
}

type roleCounts struct {
	mu     sync.Mutex
	values map[string]int
}

type roleSupervisor struct {
	name   string
	counts *roleCounts
}

func (s roleSupervisor) Run(context.Context) error {
	s.counts.mu.Lock()
	defer s.counts.mu.Unlock()
	s.counts.values[s.name]++
	return nil
}

func sameCounts(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}
