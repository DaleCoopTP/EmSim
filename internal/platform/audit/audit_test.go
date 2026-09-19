package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func validEntry() Entry {
	actor := uuid.New()
	resource := uuid.New()
	return Entry{
		ActorID:      &actor,
		ActorRole:    "instructor",
		Action:       "auth.login",
		ResourceType: "user",
		ResourceID:   &resource,
		Outcome:      OutcomeOK,
		RequestID:    "req-1",
		Details:      map[string]any{"reason": "ok"},
	}
}

func TestEntryValidateAcceptsWellFormedEntry(t *testing.T) {
	if err := validEntry().validate(); err != nil {
		t.Fatalf("validate() error = %v, want nil", err)
	}
}

func TestEntryValidateAcceptsSystemActor(t *testing.T) {
	e := validEntry()
	e.ActorID = nil
	e.ActorRole = ""
	if err := e.validate(); err != nil {
		t.Fatalf("validate() error = %v, want nil for a system action", err)
	}
}

func TestEntryValidateAcceptsMultiSegmentAction(t *testing.T) {
	e := validEntry()
	e.Action = "admin.user.create"
	if err := e.validate(); err != nil {
		t.Fatalf("validate() error = %v, want nil", err)
	}
}

func TestEntryValidateRejectsMalformedAction(t *testing.T) {
	tests := []string{"", "login", "Auth.Login", "auth.", ".login", "auth login"}
	for _, action := range tests {
		e := validEntry()
		e.Action = action
		if err := e.validate(); !errors.Is(err, ErrInvalidEntry) {
			t.Errorf("validate() action=%q error = %v, want ErrInvalidEntry", action, err)
		}
	}
}

func TestEntryValidateRejectsMalformedResourceType(t *testing.T) {
	tests := []string{"", "User", "user-account", "1user", "user type"}
	for _, resourceType := range tests {
		e := validEntry()
		e.ResourceType = resourceType
		if err := e.validate(); !errors.Is(err, ErrInvalidEntry) {
			t.Errorf("validate() resource_type=%q error = %v, want ErrInvalidEntry", resourceType, err)
		}
	}
}

func TestEntryValidateRejectsUnknownOutcome(t *testing.T) {
	e := validEntry()
	e.Outcome = Outcome("success")
	if err := e.validate(); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("validate() error = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryValidateRejectsForbiddenDetailKeys(t *testing.T) {
	tests := []string{"password", "Password", "PASSWORD", "full_name", "Full_Name", "login", "Login"}
	for _, key := range tests {
		e := validEntry()
		e.Details = map[string]any{key: "leaked"}
		if err := e.validate(); !errors.Is(err, ErrInvalidEntry) {
			t.Errorf("validate() details key=%q error = %v, want ErrInvalidEntry", key, err)
		}
	}
}

func TestEntryValidateAllowsUnrelatedDetailKeys(t *testing.T) {
	e := validEntry()
	e.Details = map[string]any{"field": "workstation_no", "attempt": 3}
	if err := e.validate(); err != nil {
		t.Fatalf("validate() error = %v, want nil", err)
	}
}

func TestRecordRejectsNilTx(t *testing.T) {
	err := Record(context.Background(), nil, validEntry())
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("Record() error = %v, want ErrInvalidEntry", err)
	}
}
