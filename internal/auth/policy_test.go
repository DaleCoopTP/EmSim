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

	if _, err := service.UpdateUser(ctx, instructor.ID, Patch{Password: strPtr("admin-reset-pass-1")}, actor, "r"); err != nil {
		t.Fatal(err)
	}
	reset, _ := store.UserByID(ctx, nil, instructor.ID)
	if !reset.MustChangePassword {
		t.Fatal("admin password reset did not set must_change_password")
	}
}

func strPtr(s string) *string { return &s }

func TestImportUsersIsAllOrNothingWithGeneratedPasswords(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	existing, _ := testTrainee("already-here", "correct-horse", "dds_district")
	store.addUser(existing)
	service := NewService(store, NewPasswordIdentityProvider(store), time.Hour, NewLoginLimiter(100, time.Minute, nil), fakeCatalog{known: map[string]bool{"dds_district": true}}).WithPolicy(DefaultPolicy())
	ctx := context.Background()
	code, bad := "dds_district", "nope"

	_, err := service.ImportUsers(ctx, []ImportRow{
		{Row: 1, Login: "fresh-one", FullName: "A", Role: RoleTrainee, ServiceCode: &code},
		{Row: 2, Login: "already-here", FullName: "B", Role: RoleTrainee},
		{Row: 3, Login: "fresh-one", FullName: "C", Role: RoleTrainee},
		{Row: 4, Login: "fresh-two", FullName: "D", Role: RoleTrainee, ServiceCode: &bad},
		{Row: 5, Login: "fresh-three", FullName: "E", Role: "boss"},
	}, false, actor, "r")
	var importErr *ImportError
	if !errors.As(err, &importErr) || len(importErr.Issues) != 4 {
		t.Fatalf("err = %v (%+v), want an ImportError with four issues", err, importErr)
	}
	if _, err := store.UserByLogin(ctx, nil, "fresh-one"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a bad file created a user")
	}

	good := []ImportRow{
		{Row: 1, Login: "class-a", FullName: "Первый", Role: RoleTrainee, ServiceCode: &code},
		{Row: 2, Login: "class-b", FullName: "Второй", Role: RoleInstructor},
	}
	dry, err := service.ImportUsers(ctx, good, true, actor, "r")
	if err != nil || len(dry) != 2 || dry[0].Password != "" {
		t.Fatalf("dry run = %+v %v, want two users without passwords", dry, err)
	}
	if _, err := store.UserByLogin(ctx, nil, "class-a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("a dry run created a user")
	}

	created, err := service.ImportUsers(ctx, good, false, actor, "r")
	if err != nil || len(created) != 2 {
		t.Fatalf("import = %+v %v", created, err)
	}
	if created[0].Password == "" || created[0].Password == created[1].Password || len(created[0].Password) < 14 {
		t.Fatalf("passwords = %q %q, want two distinct 14+ character passwords", created[0].Password, created[1].Password)
	}
	if len(store.auditEntriesByAction("admin.user.import")) != 1 {
		t.Fatal("import is not audited exactly once")
	}
	for _, u := range created {
		stored, err := store.UserByLogin(ctx, nil, u.Login)
		if err != nil {
			t.Fatal(err)
		}
		if ok, _ := VerifyPassword(stored.PasswordHash, u.Password); !ok {
			t.Fatalf("%s: the returned password does not match the stored hash", u.Login)
		}
		if want := u.Role == RoleInstructor; stored.MustChangePassword != want {
			t.Fatalf("%s: must_change_password = %v, want %v", u.Login, stored.MustChangePassword, want)
		}
	}
}
