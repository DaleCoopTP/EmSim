// New tests: HandlerRegistry is new in this port (core's Runner took one
// Handler because it only ever ran one Kind; see runner.go).
package tasks

import (
	"context"
	"errors"
	"testing"
)

func TestHandlerRegistryDispatchesByKindAndRejectsUnknown(t *testing.T) {
	registry := NewHandlerRegistry()
	var handledKind Kind
	handler := HandlerFunc(func(_ context.Context, lease Lease) error {
		handledKind = lease.Kind
		return nil
	})
	if err := registry.Register("system.noop", handler); err != nil {
		t.Fatalf("register handler: %v", err)
	}
	if err := registry.Register("system.noop", handler); !errors.Is(err, ErrDuplicateKind) {
		t.Fatalf("duplicate register error = %v", err)
	}
	if err := registry.Register("bad kind", handler); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("bad kind name error = %v", err)
	}

	if err := registry.Handle(context.Background(), Lease{Kind: "system.noop"}); err != nil {
		t.Fatalf("dispatch to registered kind: %v", err)
	}
	if handledKind != "system.noop" {
		t.Fatalf("handled kind = %q, want system.noop", handledKind)
	}
	if err := registry.Handle(context.Background(), Lease{Kind: "system.unregistered"}); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("dispatch to unregistered kind error = %v", err)
	}
}
