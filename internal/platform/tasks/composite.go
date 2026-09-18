// Ported from orchestration-core@34290d74656bc594f8968ae17871dc997559b497
// internal/worker/roles.go; adapted: core's roles were dialogue|judge|
// maintenance|all, one Runner per domain kind. This port has an open kind
// registry instead (kinds.go), so a process's role is just worker (run
// every registered pool's Runner) or maintenance (reaper + sampler, added
// with observability in a later commit), or all of both — Composite
// itself, which combines an arbitrary set of Supervisors and stops them
// together on the first failure, is unchanged.
package tasks

import (
	"context"
	"errors"
	"sync"
)

var ErrInvalidRole = errors.New("invalid worker role")

type Role string

const (
	RoleWorker      Role = "worker"
	RoleMaintenance Role = "maintenance"
	RoleAll         Role = "all"
)

func ParseRole(value string) (Role, error) {
	role := Role(value)
	switch role {
	case RoleWorker, RoleMaintenance, RoleAll:
		return role, nil
	default:
		return "", ErrInvalidRole
	}
}

type Supervisor interface {
	Run(context.Context) error
}

type Components struct {
	Worker      Supervisor
	Maintenance Supervisor
}

type compositeSupervisor struct{ supervisors []Supervisor }

func Composite(supervisors ...Supervisor) (Supervisor, error) {
	if len(supervisors) == 0 {
		return nil, ErrInvalidRole
	}
	for _, supervisor := range supervisors {
		if supervisor == nil {
			return nil, ErrInvalidRole
		}
	}
	return compositeSupervisor{supervisors: append([]Supervisor(nil), supervisors...)}, nil
}

func (c compositeSupervisor) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(c.supervisors))
	var group sync.WaitGroup
	for _, supervisor := range c.supervisors {
		supervisor := supervisor
		group.Add(1)
		go func() { defer group.Done(); results <- supervisor.Run(runCtx) }()
	}
	first := <-results
	cancel()
	for index := 1; index < len(c.supervisors); index++ {
		if err := <-results; first == nil && err != nil {
			first = err
		}
	}
	group.Wait()
	return first
}

func Select(role Role, components Components) ([]Supervisor, error) {
	var selected []Supervisor
	switch role {
	case RoleWorker:
		selected = []Supervisor{components.Worker}
	case RoleMaintenance:
		selected = []Supervisor{components.Maintenance}
	case RoleAll:
		selected = []Supervisor{components.Worker, components.Maintenance}
	default:
		return nil, ErrInvalidRole
	}
	for _, supervisor := range selected {
		if supervisor == nil {
			return nil, ErrInvalidRole
		}
	}
	return selected, nil
}

func RunRole(ctx context.Context, role Role, components Components) error {
	selected, err := Select(role, components)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(selected))
	var group sync.WaitGroup
	for _, supervisor := range selected {
		supervisor := supervisor
		group.Add(1)
		go func() {
			defer group.Done()
			results <- supervisor.Run(runCtx)
		}()
	}
	var first error
	for index := range selected {
		if current := <-results; current != nil && first == nil {
			first = current
		}
		if index == 0 {
			cancel()
		}
	}
	group.Wait()
	return first
}
