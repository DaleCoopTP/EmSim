package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type stubIdentityProvider struct {
	user     User
	err      error
	login    string
	password string
}

func (p *stubIdentityProvider) Authenticate(_ context.Context, login, password string) (User, error) {
	p.login, p.password = login, password
	return p.user, p.err
}

func testAdmin(login, password string) (User, string) {
	hash, err := HashPassword(password, testParams)
	if err != nil {
		panic(err)
	}
	return User{ID: uuid.New(), Login: login, PasswordHash: hash, FullName: "Admin Adminovich", Role: RoleAdmin, Level: LevelEasy, Active: true}, password
}

func testTrainee(login, password, serviceCode string) (User, string) {
	hash, err := HashPassword(password, testParams)
	if err != nil {
		panic(err)
	}
	code := serviceCode
	return User{
		ID: uuid.New(), Login: login, PasswordHash: hash, FullName: "Иванов Иван", Role: RoleTrainee,
		ServiceCode: &code, Level: LevelEasy, Active: true,
	}, password
}

func newTestService(store *fakeStore) *Service {
	return NewService(store, NewPasswordIdentityProvider(store), time.Hour, NewLoginLimiter(5, time.Minute, nil), nil)
}

// newTestServiceWithCatalog is newTestService plus a ServiceCatalog —
// only the service_code-checking tests (admin_test.go) need a non-nil
// one; every other test's nil catalog makes checkServiceCode a no-op.
func newTestServiceWithCatalog(store *fakeStore, catalog ServiceCatalog) *Service {
	return NewService(store, NewPasswordIdentityProvider(store), time.Hour, NewLoginLimiter(5, time.Minute, nil), catalog)
}

// fakeCatalog is an in-memory ServiceCatalog for CreateUser/UpdateUser's
// service_code tests.
type fakeCatalog struct {
	known map[string]bool
	err   error
	check func()
}

func (c fakeCatalog) ServiceExists(_ context.Context, code string) (bool, error) {
	if c.check != nil {
		c.check()
	}
	if c.err != nil {
		return false, c.err
	}
	return c.known[code], nil
}

func TestServiceLoginSucceedsForAdminWithoutWorkstation(t *testing.T) {
	store := newFakeStore()
	user, password := testAdmin("dispatcher-admin", "correct-horse")
	store.addUser(user)

	result, err := newTestService(store).Login(context.Background(), LoginRequest{Login: "dispatcher-admin", Password: password}, "req-1")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.Token == "" {
		t.Fatal("Token is empty")
	}
	if result.Me.User.ID != user.ID {
		t.Fatalf("Me.User.ID = %v, want %v", result.Me.User.ID, user.ID)
	}
	if result.Me.Workstation != nil {
		t.Fatalf("Me.Workstation = %v, want nil for admin", result.Me.Workstation)
	}
	if result.ExpiresAt.Before(time.Now()) {
		t.Fatalf("ExpiresAt = %v, want a future time", result.ExpiresAt)
	}

	entries := store.auditEntriesByAction("auth.login")
	if len(entries) != 1 || entries[0].Outcome != "ok" || entries[0].RequestID != "req-1" {
		t.Fatalf("audit entries = %+v, want one ok entry with request_id=req-1", entries)
	}
}

func TestServiceLoginUsesConfiguredIdentityProvider(t *testing.T) {
	store := newFakeStore()
	user, _ := testAdmin("federated-admin", "unused-local-password")
	provider := &stubIdentityProvider{user: user}
	service := NewService(store, provider, time.Hour, NewLoginLimiter(5, time.Minute, nil), nil)

	if _, err := service.Login(context.Background(), LoginRequest{Login: "external-login", Password: "external-secret"}, "req-idp"); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if provider.login != "external-login" || provider.password != "external-secret" {
		t.Fatalf("IdentityProvider called with %q/%q", provider.login, provider.password)
	}
}

func TestServiceLoginSucceedsForTraineeWithWorkstation(t *testing.T) {
	store := newFakeStore()
	user, password := testTrainee("dispatcher-trainee", "correct-horse", "dds_district")
	store.addUser(user)
	workstation := Workstation{ID: uuid.New(), Number: 5, Label: "РМ-05", Active: true}
	store.addWorkstation(workstation)

	number := 5
	result, err := newTestService(store).Login(context.Background(), LoginRequest{
		Login: "dispatcher-trainee", Password: password, WorkstationNo: &number,
	}, "req-2")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.Me.Workstation == nil || result.Me.Workstation.ID != workstation.ID {
		t.Fatalf("Me.Workstation = %+v, want %+v", result.Me.Workstation, workstation)
	}
}

func TestServiceLoginIgnoresWorkstationNoForAdminAndInstructor(t *testing.T) {
	store := newFakeStore()
	admin, adminPassword := testAdmin("dispatcher-ignore", "correct-horse")
	store.addUser(admin)
	// No workstation 99 registered at all — if Login tried to resolve it,
	// this would fail with ErrWorkstationUnknown; it must not even look.
	number := 99
	result, err := newTestService(store).Login(context.Background(), LoginRequest{
		Login: "dispatcher-ignore", Password: adminPassword, WorkstationNo: &number,
	}, "req-3")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.Me.Workstation != nil {
		t.Fatalf("Me.Workstation = %v, want nil (workstation_no ignored for admin)", result.Me.Workstation)
	}
}

func TestServiceLoginRejectsUnknownLogin(t *testing.T) {
	store := newFakeStore()
	_, err := newTestService(store).Login(context.Background(), LoginRequest{Login: "nobody", Password: "whatever1"}, "req-4")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
	entries := store.auditEntriesByAction("auth.login")
	if len(entries) != 1 || entries[0].Outcome != "rejected" || entries[0].Details["reason"] != "invalid_credentials" {
		t.Fatalf("audit entries = %+v, want one rejected/invalid_credentials entry", entries)
	}
	if len(store.sessions) != 0 {
		t.Fatalf("sessions = %v, want none created", store.sessions)
	}
}

func TestServiceLoginRejectsWrongPassword(t *testing.T) {
	store := newFakeStore()
	user, _ := testAdmin("dispatcher-wrong", "correct-horse")
	store.addUser(user)
	_, err := newTestService(store).Login(context.Background(), LoginRequest{Login: "dispatcher-wrong", Password: "not-the-password"}, "req-5")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}

func TestServiceLoginRejectsInactiveUser(t *testing.T) {
	store := newFakeStore()
	user, password := testAdmin("dispatcher-inactive", "correct-horse")
	user.Active = false
	store.addUser(user)
	_, err := newTestService(store).Login(context.Background(), LoginRequest{Login: "dispatcher-inactive", Password: password}, "req-6")
	if !errors.Is(err, ErrUserInactive) {
		t.Fatalf("Login() error = %v, want ErrUserInactive", err)
	}
	entries := store.auditEntriesByAction("auth.login")
	if len(entries) != 1 || entries[0].Details["reason"] != "user_inactive" {
		t.Fatalf("audit entries = %+v, want reason=user_inactive", entries)
	}
}

func TestServiceLoginRejectsTraineeWithoutWorkstation(t *testing.T) {
	store := newFakeStore()
	user, password := testTrainee("dispatcher-noworkstation", "correct-horse", "dds_district")
	store.addUser(user)
	_, err := newTestService(store).Login(context.Background(), LoginRequest{Login: "dispatcher-noworkstation", Password: password}, "req-7")
	if !errors.Is(err, ErrWorkstationRequired) {
		t.Fatalf("Login() error = %v, want ErrWorkstationRequired", err)
	}
}

func TestServiceLoginRejectsUnknownWorkstation(t *testing.T) {
	store := newFakeStore()
	user, password := testTrainee("dispatcher-unknownws", "correct-horse", "dds_district")
	store.addUser(user)
	number := 42
	_, err := newTestService(store).Login(context.Background(), LoginRequest{Login: "dispatcher-unknownws", Password: password, WorkstationNo: &number}, "req-8")
	if !errors.Is(err, ErrWorkstationUnknown) {
		t.Fatalf("Login() error = %v, want ErrWorkstationUnknown", err)
	}
}

func TestServiceLoginRejectsInactiveWorkstation(t *testing.T) {
	store := newFakeStore()
	user, password := testTrainee("dispatcher-inactivews", "correct-horse", "dds_district")
	store.addUser(user)
	store.addWorkstation(Workstation{ID: uuid.New(), Number: 3, Label: "РМ-03", Active: false})
	number := 3
	_, err := newTestService(store).Login(context.Background(), LoginRequest{Login: "dispatcher-inactivews", Password: password, WorkstationNo: &number}, "req-9")
	if !errors.Is(err, ErrWorkstationInactive) {
		t.Fatalf("Login() error = %v, want ErrWorkstationInactive", err)
	}
}

func TestServiceLoginRateLimitsAfterMaxAttempts(t *testing.T) {
	store := newFakeStore()
	service := NewService(store, NewPasswordIdentityProvider(store), time.Hour, NewLoginLimiter(2, time.Minute, nil), nil)
	for i := 0; i < 2; i++ {
		_, err := service.Login(context.Background(), LoginRequest{Login: "someone", Password: "wrong-password"}, "req-rl")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d error = %v, want ErrInvalidCredentials", i+1, err)
		}
	}
	_, err := service.Login(context.Background(), LoginRequest{Login: "someone", Password: "wrong-password"}, "req-rl")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("3rd attempt error = %v, want ErrRateLimited", err)
	}
	entries := store.auditEntriesByAction("auth.login")
	if entries[len(entries)-1].Details["reason"] != "rate_limited" {
		t.Fatalf("last audit entry = %+v, want reason=rate_limited", entries[len(entries)-1])
	}
}

func TestServiceLoginStorageFailureRollsBackWithoutAudit(t *testing.T) {
	store := newFakeStore()
	user, password := testAdmin("dispatcher-storagefail", "correct-horse")
	store.addUser(user)
	store.failInsertSession = true

	_, err := newTestService(store).Login(context.Background(), LoginRequest{Login: "dispatcher-storagefail", Password: password}, "req-10")
	if !errors.Is(err, ErrStorage) {
		t.Fatalf("Login() error = %v, want ErrStorage", err)
	}
	if len(store.sessions) != 0 {
		t.Fatalf("sessions = %v, want none after a failed insert", store.sessions)
	}
	if entries := store.auditEntriesByAction("auth.login"); len(entries) != 0 {
		t.Fatalf("audit entries = %+v, want none (nothing to roll back to, but also nothing recorded)", entries)
	}
}

func TestServiceLoginThenAuthenticateRoundTrip(t *testing.T) {
	store := newFakeStore()
	user, password := testAdmin("dispatcher-roundtrip", "correct-horse")
	store.addUser(user)
	service := newTestService(store)

	result, err := service.Login(context.Background(), LoginRequest{Login: "dispatcher-roundtrip", Password: password}, "req-11")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	principal, err := service.Authenticate(context.Background(), result.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if principal.UserID != user.ID || principal.Role != RoleAdmin {
		t.Fatalf("Authenticate() = %+v, want user %v role admin", principal, user.ID)
	}
	if principal.WorkstationID != nil {
		t.Fatalf("WorkstationID = %v, want nil", principal.WorkstationID)
	}
}

func TestServiceAuthenticateRejectsGarbageToken(t *testing.T) {
	store := newFakeStore()
	service := newTestService(store)
	for _, token := range []string{"", "not-base64!!!", "dG9vLXNob3J0"} {
		if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrSessionInvalid) {
			t.Errorf("Authenticate(%q) error = %v, want ErrSessionInvalid", token, err)
		}
	}
}

func TestServiceAuthenticateRejectsUnknownSession(t *testing.T) {
	store := newFakeStore()
	service := newTestService(store)
	fakeToken := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // valid shape, never issued
	if _, err := service.Authenticate(context.Background(), fakeToken); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("Authenticate() error = %v, want ErrSessionInvalid", err)
	}
}

func TestServiceAuthenticateRejectsSessionOfInactiveUser(t *testing.T) {
	store := newFakeStore()
	user, password := testAdmin("dispatcher-deactivated", "correct-horse")
	store.addUser(user)
	service := newTestService(store)

	result, err := service.Login(context.Background(), LoginRequest{Login: "dispatcher-deactivated", Password: password}, "req-12")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	deactivated := user
	deactivated.Active = false
	store.addUser(deactivated)

	if _, err := service.Authenticate(context.Background(), result.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("Authenticate() for a deactivated user's session error = %v, want ErrSessionInvalid", err)
	}
}

func TestServiceAuthenticateAppliesSlidingRenewalOnlyWhenStale(t *testing.T) {
	store := newFakeStore()
	user, password := testAdmin("dispatcher-renew", "correct-horse")
	store.addUser(user)
	service := newTestService(store)

	result, err := service.Login(context.Background(), LoginRequest{Login: "dispatcher-renew", Password: password}, "req-13")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	principal, err := service.Authenticate(context.Background(), result.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !principal.SessionExpiresAt.Equal(result.ExpiresAt) {
		t.Fatalf("a fresh session was renewed: ExpiresAt = %v, want unchanged %v", principal.SessionExpiresAt, result.ExpiresAt)
	}

	// Backdate LastSeenAt past staleAfter directly in the fake, then touch
	// again: this time it must renew.
	id, err := sessionIDFromToken(result.Token)
	if err != nil {
		t.Fatalf("sessionIDFromToken() error = %v", err)
	}
	store.mu.Lock()
	session := store.sessions[string(id)]
	session.LastSeenAt = time.Now().Add(-10 * time.Minute)
	store.sessions[string(id)] = session
	store.mu.Unlock()

	principal2, err := service.Authenticate(context.Background(), result.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if !principal2.SessionExpiresAt.After(result.ExpiresAt) {
		t.Fatalf("ExpiresAt = %v, want it renewed past %v", principal2.SessionExpiresAt, result.ExpiresAt)
	}
}

func TestServiceLogoutDeletesSessionAndAudits(t *testing.T) {
	store := newFakeStore()
	user, password := testAdmin("dispatcher-logout", "correct-horse")
	store.addUser(user)
	service := newTestService(store)

	result, err := service.Login(context.Background(), LoginRequest{Login: "dispatcher-logout", Password: password}, "req-14")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	if err := service.Logout(context.Background(), result.Token, "req-15"); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}

	if _, err := service.Authenticate(context.Background(), result.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("Authenticate() after Logout error = %v, want ErrSessionInvalid", err)
	}
	entries := store.auditEntriesByAction("auth.logout")
	if len(entries) != 1 || entries[0].RequestID != "req-15" {
		t.Fatalf("audit entries = %+v, want one auth.logout entry with request_id=req-15", entries)
	}
}

func TestServiceLogoutIsIdempotentForUnknownToken(t *testing.T) {
	store := newFakeStore()
	service := newTestService(store)
	// Must not panic and must not record anything for a token that was
	// never issued — logout is unconditionally idempotent (openapi.yaml:
	// POST /auth/logout always 204).
	if err := service.Logout(context.Background(), "garbage-token", "req-16"); err != nil {
		t.Fatalf("Logout(garbage) error = %v", err)
	}
	if err := service.Logout(context.Background(), "", "req-17"); err != nil {
		t.Fatalf("Logout(empty) error = %v", err)
	}
	if entries := store.auditEntriesByAction("auth.logout"); len(entries) != 0 {
		t.Fatalf("audit entries = %+v, want none for an unknown/empty token", entries)
	}
}

func TestServiceLogoutReturnsStorageFailureAndRollsBackDeletion(t *testing.T) {
	store := newFakeStore()
	user, password := testAdmin("dispatcher-logout-failure", "correct-horse")
	store.addUser(user)
	service := newTestService(store)
	result, err := service.Login(context.Background(), LoginRequest{Login: user.Login, Password: password}, "req-logout-login")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	store.failAuditRecord = true
	if err := service.Logout(context.Background(), result.Token, "req-logout-failure"); !errors.Is(err, ErrStorage) {
		t.Fatalf("Logout() error = %v, want ErrStorage", err)
	}
	store.failAuditRecord = false
	if _, err := service.Authenticate(context.Background(), result.Token); err != nil {
		t.Fatalf("session was not restored after failed logout transaction: %v", err)
	}
}

func TestServiceMeBuildsFullReadModel(t *testing.T) {
	store := newFakeStore()
	user, password := testTrainee("dispatcher-me", "correct-horse", "dds_district")
	store.addUser(user)
	workstation := Workstation{ID: uuid.New(), Number: 8, Label: "РМ-08", Active: true}
	store.addWorkstation(workstation)
	service := newTestService(store)

	number := 8
	result, err := service.Login(context.Background(), LoginRequest{Login: "dispatcher-me", Password: password, WorkstationNo: &number}, "req-18")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	principal, err := service.Authenticate(context.Background(), result.Token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	me, err := service.Me(context.Background(), principal)
	if err != nil {
		t.Fatalf("Me() error = %v", err)
	}
	if me.User.ID != user.ID || me.Workstation == nil || me.Workstation.ID != workstation.ID {
		t.Fatalf("Me() = %+v, want user %v with workstation %v", me, user.ID, workstation.ID)
	}
	if me.SessionExpiresAt.IsZero() {
		t.Fatal("Me().SessionExpiresAt is zero")
	}
}

func TestServiceLoginNeverLogsPasswordOrHash(t *testing.T) {
	store := newFakeStore()
	user, _ := testAdmin("dispatcher-secret", "correct-horse-battery")
	store.addUser(user)

	_, err := newTestService(store).Login(context.Background(), LoginRequest{Login: "dispatcher-secret", Password: "wrong-one"}, "req-19")
	if err == nil || err.Error() == "" {
		t.Fatalf("Login() error = %v", err)
	}
	if strings.Contains(err.Error(), "wrong-one") || strings.Contains(err.Error(), user.PasswordHash) {
		t.Fatalf("Login() error leaks credential material: %v", err)
	}
	for _, entry := range store.auditEntries {
		for _, v := range entry.Details {
			if s, ok := v.(string); ok && (strings.Contains(s, "wrong-one") || strings.Contains(s, user.PasswordHash)) {
				t.Fatalf("audit entry leaks credential material: %+v", entry)
			}
		}
	}
}
