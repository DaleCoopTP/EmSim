package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func policyService(store *fakeStore) *Service {
	return NewService(store, NewPasswordIdentityProvider(store), time.Hour, NewLoginLimiter(100, time.Minute, nil), nil).WithPolicy(Policy{
		LockoutAttempts: 3, LockoutDuration: time.Hour, PasswordMinLength: 12,
		ForceChangeRoles: []Role{RoleInstructor},
	})
}

// TestLoginLocksAfterRunOfWrongPasswords (ADR-038): the third wrong
// password locks the account; while locked even the right password is
// refused with ErrAccountLocked; an administrator's unlock lets it in.
func TestLoginLocksAfterRunOfWrongPasswords(t *testing.T) {
	store := newFakeStore()
	user, password := testAdmin("policy-admin", "correct-horse-battery")
	store.addUser(user)
	service := policyService(store)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := service.Login(ctx, LoginRequest{Login: "policy-admin", Password: "wrong"}, "r"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v, want invalid credentials", i+1, err)
		}
	}
	if _, err := service.Login(ctx, LoginRequest{Login: "policy-admin", Password: password}, "r"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("correct password while locked: %v, want ErrAccountLocked", err)
	}
	if len(store.auditEntriesByAction("auth.lockout")) != 1 {
		t.Fatalf("lockout audit rows = %d, want 1", len(store.auditEntriesByAction("auth.lockout")))
	}

	actor := testAdminActor(store)
	if _, err := service.UpdateUser(ctx, user.ID, Patch{Unlock: true}, actor, "r"); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if _, err := service.Login(ctx, LoginRequest{Login: "policy-admin", Password: password}, "r"); err != nil {
		t.Fatalf("login after unlock: %v", err)
	}
	if len(store.auditEntriesByAction("admin.user.unlock")) != 1 {
		t.Fatal("unlock is not audited")
	}
}

// TestSuccessfulLoginEndsTheRun: two wrong passwords, a good login, then two
// more wrong ones must not lock (the run restarted).
func TestSuccessfulLoginEndsTheRun(t *testing.T) {
	store := newFakeStore()
	user, password := testAdmin("run-admin", "correct-horse-battery")
	store.addUser(user)
	service := policyService(store)
	ctx := context.Background()
	wrong := func() {
		t.Helper()
		if _, err := service.Login(ctx, LoginRequest{Login: "run-admin", Password: "wrong"}, "r"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("wrong password: %v", err)
		}
	}
	wrong()
	wrong()
	if _, err := service.Login(ctx, LoginRequest{Login: "run-admin", Password: password}, "r"); err != nil {
		t.Fatal(err)
	}
	wrong()
	wrong()
	if _, err := service.Login(ctx, LoginRequest{Login: "run-admin", Password: password}, "r"); err != nil {
		t.Fatalf("two wrong passwords after a good login locked the account: %v", err)
	}
}

// TestPolicyForcesPasswordChangeForNamedRoles: an administrator-created or
// reset password is temporary for the configured roles only, and the
// user's own change lifts the flag and ends their other sessions.
func TestPolicyForcesPasswordChangeForNamedRoles(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	service := policyService(store)
	ctx := context.Background()

	if _, err := service.CreateUser(ctx, NewUser{Login: "short-pw", Password: "short-pass", FullName: "A", Role: RoleInstructor}, actor, "r"); err == nil {
		t.Fatal("a password below PASSWORD_MIN_LENGTH was accepted")
	}
	instructor, err := service.CreateUser(ctx, NewUser{Login: "instr-force", Password: "twelve-chars-ok", FullName: "Instr", Role: RoleInstructor}, actor, "r")
	if err != nil || !instructor.MustChangePassword {
		t.Fatalf("instructor: %+v %v, want must_change_password", instructor, err)
	}
	trainee, err := service.CreateUser(ctx, NewUser{Login: "trainee-free", Password: "twelve-chars-ok", FullName: "Trainee", Role: RoleTrainee}, actor, "r")
	if err != nil || trainee.MustChangePassword {
		t.Fatalf("trainee: %+v %v, want no forced change", trainee, err)
	}

	first, err := service.Login(ctx, LoginRequest{Login: "instr-force", Password: "twelve-chars-ok"}, "r")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(ctx, LoginRequest{Login: "instr-force", Password: "twelve-chars-ok"}, "r")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(ctx, first.Token)
	if err != nil || !principal.MustChangePassword {
		t.Fatalf("principal = %+v %v, want must-change", principal, err)
	}

	if err := service.ChangePassword(ctx, principal, "not-the-password", "another-long-pass", "r"); err == nil {
		t.Fatal("wrong current password accepted")
	}
	if err := service.ChangePassword(ctx, principal, "twelve-chars-ok", "twelve-chars-ok", "r"); err == nil {
		t.Fatal("unchanged password accepted")
	}
	if err := service.ChangePassword(ctx, principal, "twelve-chars-ok", "another-long-pass", "r"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	after, err := service.Authenticate(ctx, first.Token)
	if err != nil || after.MustChangePassword {
		t.Fatalf("own session after change: %+v %v, want still valid and free", after, err)
	}
	if _, err := service.Authenticate(ctx, second.Token); err == nil {
		t.Fatal("the other session survived a password change")
	}

	// An administrator's reset makes the new password temporary again.
	if _, err := service.UpdateUser(ctx, instructor.ID, Patch{Password: strPtr("admin-reset-pass-1")}, actor, "r"); err != nil {
		t.Fatal(err)
	}
	reset, _ := store.UserByID(ctx, nil, instructor.ID)
	if !reset.MustChangePassword {
		t.Fatal("admin password reset did not set must_change_password")
	}
}

func strPtr(s string) *string { return &s }
