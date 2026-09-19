package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func testAdminActor(store *fakeStore) Principal {
	user, _ := testAdmin("acting-admin", "correct-horse")
	store.addUser(user)
	return Principal{UserID: user.ID, Role: RoleAdmin}
}

func TestCreateUserInsertsAndAudits(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	service := newTestService(store)

	code := "dds_district"
	created, err := service.CreateUser(context.Background(), NewUser{
		Login: "dispatcher-new", Password: "correct-horse", FullName: "Иванов Иван",
		Role: RoleTrainee, ServiceCode: &code,
	}, actor, "req-create-1")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if created.Login != "dispatcher-new" || created.Role != RoleTrainee || !created.Active {
		t.Fatalf("created = %+v", created)
	}
	if created.Level != LevelEasy {
		t.Fatalf("Level = %q, want easy default", created.Level)
	}
	if created.PasswordHash == "" || created.PasswordHash == "correct-horse" {
		t.Fatalf("PasswordHash = %q, want a hash, not the plaintext", created.PasswordHash)
	}
	ok, err := VerifyPassword(created.PasswordHash, "correct-horse")
	if err != nil || !ok {
		t.Fatalf("stored hash does not verify the given password: ok=%v err=%v", ok, err)
	}

	entries := store.auditEntriesByAction("admin.user.create")
	if len(entries) != 1 || entries[0].RequestID != "req-create-1" || *entries[0].ResourceID != created.ID {
		t.Fatalf("audit entries = %+v", entries)
	}
}

func TestCreateUserRejectsInvalidInput(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	service := newTestService(store)

	_, err := service.CreateUser(context.Background(), NewUser{Login: "bad login", Password: "correct-horse", FullName: "x", Role: RoleAdmin}, actor, "req-create-2")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("CreateUser() error = %v, want *ValidationError", err)
	}
	if len(store.auditEntriesByAction("admin.user.create")) != 0 {
		t.Fatal("audit entry recorded for a rejected create")
	}
}

func TestCreateUserRejectsDuplicateLogin(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	existing, _ := testAdmin("dispatcher-dup", "correct-horse")
	store.addUser(existing)
	service := newTestService(store)

	_, err := service.CreateUser(context.Background(), NewUser{
		Login: "dispatcher-dup", Password: "correct-horse", FullName: "x", Role: RoleAdmin,
	}, actor, "req-create-3")
	if !errors.Is(err, ErrLoginTaken) {
		t.Fatalf("CreateUser() error = %v, want ErrLoginTaken", err)
	}
}

func TestCreateUserRejectsUnknownServiceCode(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	service := newTestServiceWithCatalog(store, fakeCatalog{known: map[string]bool{"dds_district": true}})

	code := "no_such_service"
	_, err := service.CreateUser(context.Background(), NewUser{
		Login: "dispatcher-unknown-svc", Password: "correct-horse", FullName: "x",
		Role: RoleTrainee, ServiceCode: &code,
	}, actor, "req-create-4")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "service_code" || ve.Reason != "unknown" {
		t.Fatalf("CreateUser() error = %v, want *ValidationError{service_code, unknown}", err)
	}
}

func TestCreateUserAllowsKnownServiceCode(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	service := newTestServiceWithCatalog(store, fakeCatalog{known: map[string]bool{"dds_district": true}})

	code := "dds_district"
	created, err := service.CreateUser(context.Background(), NewUser{
		Login: "dispatcher-known-svc", Password: "correct-horse", FullName: "x",
		Role: RoleTrainee, ServiceCode: &code,
	}, actor, "req-create-5")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if created.ServiceCode == nil || *created.ServiceCode != code {
		t.Fatalf("created.ServiceCode = %v, want %q", created.ServiceCode, code)
	}
}

func TestCreateUserSurfacesCatalogReadFailureAsStorageError(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	service := newTestServiceWithCatalog(store, fakeCatalog{err: errors.New("db unreachable")})

	code := "dds_district"
	_, err := service.CreateUser(context.Background(), NewUser{
		Login: "dispatcher-catalog-down", Password: "correct-horse", FullName: "x",
		Role: RoleTrainee, ServiceCode: &code,
	}, actor, "req-create-6")
	if !errors.Is(err, ErrStorage) {
		t.Fatalf("CreateUser() error = %v, want ErrStorage (not masked as validation failure)", err)
	}
}

func TestUpdateUserAppliesPartialChangesAndAudits(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	target, _ := testTrainee("dispatcher-target", "correct-horse", "dds_district")
	store.addUser(target)
	service := newTestService(store)

	newName := "Новое Имя"
	updated, err := service.UpdateUser(context.Background(), target.ID, Patch{FullName: &newName}, actor, "req-update-1")
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if updated.FullName != newName || updated.Login != target.Login || updated.Role != target.Role {
		t.Fatalf("updated = %+v", updated)
	}

	entries := store.auditEntriesByAction("admin.user.update")
	if len(entries) != 1 || entries[0].RequestID != "req-update-1" {
		t.Fatalf("audit entries = %+v", entries)
	}
}

func TestUpdateUserRejectsUnknownServiceCode(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	target, _ := testTrainee("dispatcher-svc-target", "correct-horse", "dds_district")
	store.addUser(target)
	service := newTestServiceWithCatalog(store, fakeCatalog{known: map[string]bool{"dds_district": true}})

	newCode := "no_such_service"
	_, err := service.UpdateUser(context.Background(), target.ID, Patch{ServiceCode: &newCode}, actor, "req-update-svc-1")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "service_code" || ve.Reason != "unknown" {
		t.Fatalf("UpdateUser() error = %v, want *ValidationError{service_code, unknown}", err)
	}
}

// TestUpdateUserClearingServiceCodeSkipsCatalogCheck confirms Patch's
// empty-string-means-clear convention (domain.go) never reaches the
// catalog — clearing to NULL cannot be "unknown".
func TestUpdateUserClearingServiceCodeSkipsCatalogCheck(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	target, _ := testTrainee("dispatcher-svc-clear", "correct-horse", "dds_district")
	store.addUser(target)
	newRole := RoleInstructor
	service := newTestServiceWithCatalog(store, fakeCatalog{err: errors.New("must not be called")})

	empty := ""
	updated, err := service.UpdateUser(context.Background(), target.ID, Patch{Role: &newRole, ServiceCode: &empty}, actor, "req-update-svc-2")
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if updated.ServiceCode != nil {
		t.Fatalf("updated.ServiceCode = %v, want nil (cleared)", updated.ServiceCode)
	}
}

func TestUpdateUserRejectsInvalidPatch(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	target, _ := testAdmin("dispatcher-invalidpatch", "correct-horse")
	store.addUser(target)
	service := newTestService(store)

	code := "dds_district"
	_, err := service.UpdateUser(context.Background(), target.ID, Patch{ServiceCode: &code}, actor, "req-update-2")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "service_code" {
		t.Fatalf("UpdateUser() error = %v, want service_code ValidationError", err)
	}
}

func TestUpdateUserRejectsDemotingLastActiveAdmin(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	// actor is itself the only OTHER admin created so far; make it the
	// sole admin by not creating any other, and try to demote the actor.
	newRole := RoleInstructor
	_, err := newTestService(store).UpdateUser(context.Background(), actor.UserID, Patch{Role: &newRole}, actor, "req-update-3")
	if !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("UpdateUser() error = %v, want ErrLastAdmin", err)
	}
}

func TestUpdateUserAllowsDemotingWhenAnotherActiveAdminRemains(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	otherAdmin, _ := testAdmin("dispatcher-otheradmin", "correct-horse")
	store.addUser(otherAdmin)
	service := newTestService(store)

	newRole := RoleInstructor
	updated, err := service.UpdateUser(context.Background(), actor.UserID, Patch{Role: &newRole}, actor, "req-update-4")
	if err != nil {
		t.Fatalf("UpdateUser() error = %v, want nil (another active admin remains)", err)
	}
	if updated.Role != RoleInstructor {
		t.Fatalf("Role = %s, want instructor", updated.Role)
	}
}

func TestUpdateUserAllowsUnrelatedPatchOnTheLastAdmin(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	service := newTestService(store)

	newName := "Ещё Имя"
	_, err := service.UpdateUser(context.Background(), actor.UserID, Patch{FullName: &newName}, actor, "req-update-5")
	if err != nil {
		t.Fatalf("UpdateUser() error = %v, want nil (patch does not touch role/active)", err)
	}
}

func TestUpdateUserPasswordChangeInvalidatesSessions(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	target, password := testAdmin("dispatcher-pwchange", "correct-horse")
	store.addUser(target)
	service := newTestService(store)

	loginResult, err := service.Login(context.Background(), LoginRequest{Login: target.Login, Password: password}, "req-login")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), loginResult.Token); err != nil {
		t.Fatalf("Authenticate() before update error = %v", err)
	}

	newPassword := "brand-new-password"
	if _, err := service.UpdateUser(context.Background(), target.ID, Patch{Password: &newPassword}, actor, "req-update-6"); err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}

	if _, err := service.Authenticate(context.Background(), loginResult.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("Authenticate() after password change error = %v, want ErrSessionInvalid", err)
	}
}

func TestUpdateUserDeactivationInvalidatesSessions(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	target, password := testTrainee("dispatcher-deactivate", "correct-horse", "dds_district")
	store.addUser(target)
	service := newTestService(store)

	number := 1
	store.addWorkstation(Workstation{ID: uuid.New(), Number: number, Label: "РМ-01", Active: true})
	loginResult, err := service.Login(context.Background(), LoginRequest{Login: target.Login, Password: password, WorkstationNo: &number}, "req-login-2")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	inactive := false
	if _, err := service.UpdateUser(context.Background(), target.ID, Patch{Active: &inactive}, actor, "req-update-7"); err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}

	if _, err := service.Authenticate(context.Background(), loginResult.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("Authenticate() after deactivation error = %v, want ErrSessionInvalid", err)
	}
}

func TestUpdateUserUnrelatedPatchLeavesSessionsAlone(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	target, password := testAdmin("dispatcher-unrelated", "correct-horse")
	store.addUser(target)
	service := newTestService(store)

	loginResult, err := service.Login(context.Background(), LoginRequest{Login: target.Login, Password: password}, "req-login-3")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	newName := "Переименован"
	if _, err := service.UpdateUser(context.Background(), target.ID, Patch{FullName: &newName}, actor, "req-update-8"); err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}

	if _, err := service.Authenticate(context.Background(), loginResult.Token); err != nil {
		t.Fatalf("Authenticate() after an unrelated patch error = %v, want nil (session preserved)", err)
	}
}

func TestListUsersDelegatesToStore(t *testing.T) {
	store := newFakeStore()
	for _, login := range []string{"c-user", "a-user", "b-user"} {
		u, _ := testAdmin(login, "correct-horse")
		store.addUser(u)
	}
	service := newTestService(store)

	users, total, err := service.ListUsers(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if total != 3 || len(users) != 2 || users[0].Login != "a-user" || users[1].Login != "b-user" {
		t.Fatalf("ListUsers() = %v total=%d", users, total)
	}
}

func TestListWorkstationsReturnsAllOrderedByNumber(t *testing.T) {
	store := newFakeStore()
	store.addWorkstation(Workstation{ID: uuid.New(), Number: 3, Label: "РМ-03", Active: true})
	store.addWorkstation(Workstation{ID: uuid.New(), Number: 1, Label: "РМ-01", Active: false})
	service := newTestService(store)

	workstations, err := service.ListWorkstations(context.Background())
	if err != nil {
		t.Fatalf("ListWorkstations() error = %v", err)
	}
	if len(workstations) != 2 || workstations[0].Number != 1 || workstations[1].Number != 3 {
		t.Fatalf("ListWorkstations() = %+v", workstations)
	}
}

func TestReplaceWorkstationsUpsertsAndDeactivatesMissing(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	store.addWorkstation(Workstation{ID: uuid.New(), Number: 1, Label: "old РМ-01", Active: true})
	store.addWorkstation(Workstation{ID: uuid.New(), Number: 2, Label: "РМ-02", Active: true})
	service := newTestService(store)

	result, err := service.ReplaceWorkstations(context.Background(), []Workstation{
		{Number: 1, Label: "new РМ-01"},
		{Number: 3, Label: "РМ-03"},
	}, actor, "req-replace-1")
	if err != nil {
		t.Fatalf("ReplaceWorkstations() error = %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("result = %+v, want 3 rows (1 kept+renamed, 2 deactivated, 3 new)", result)
	}

	byNumber := map[int]Workstation{}
	for _, w := range result {
		byNumber[w.Number] = w
	}
	if !byNumber[1].Active || byNumber[1].Label != "new РМ-01" {
		t.Fatalf("workstation 1 = %+v", byNumber[1])
	}
	if byNumber[2].Active {
		t.Fatalf("workstation 2 = %+v, want deactivated", byNumber[2])
	}
	if !byNumber[3].Active {
		t.Fatalf("workstation 3 = %+v, want active", byNumber[3])
	}

	entries := store.auditEntriesByAction("admin.workstations.replace")
	if len(entries) != 1 || entries[0].RequestID != "req-replace-1" {
		t.Fatalf("audit entries = %+v", entries)
	}
}

func TestReplaceWorkstationsRejectsDuplicateNumbers(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	service := newTestService(store)

	_, err := service.ReplaceWorkstations(context.Background(), []Workstation{
		{Number: 1, Label: "a"}, {Number: 1, Label: "b"},
	}, actor, "req-replace-2")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Reason != "duplicate" {
		t.Fatalf("ReplaceWorkstations() error = %v, want a duplicate ValidationError", err)
	}
	if len(store.auditEntriesByAction("admin.workstations.replace")) != 0 {
		t.Fatal("audit entry recorded for a rejected replace")
	}
}

func TestReplaceWorkstationsRejectsNonPositiveNumber(t *testing.T) {
	store := newFakeStore()
	actor := testAdminActor(store)
	service := newTestService(store)

	_, err := service.ReplaceWorkstations(context.Background(), []Workstation{{Number: 0, Label: "a"}}, actor, "req-replace-3")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "number" {
		t.Fatalf("ReplaceWorkstations() error = %v, want a number ValidationError", err)
	}
}

func TestBootstrapAdminCreatesOnEmptyInstallation(t *testing.T) {
	store := newFakeStore()
	service := newTestService(store)

	created, err := service.BootstrapAdmin(context.Background(), "bootstrap-admin", "correct-horse")
	if err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	if !created {
		t.Fatal("created = false, want true on an empty installation")
	}
	u, err := store.UserByLogin(context.Background(), fakeTx{}, "bootstrap-admin")
	if err != nil {
		t.Fatalf("UserByLogin() error = %v", err)
	}
	if u.Role != RoleAdmin || !u.Active {
		t.Fatalf("created user = %+v, want an active admin", u)
	}
	ok, err := VerifyPassword(u.PasswordHash, "correct-horse")
	if err != nil || !ok {
		t.Fatalf("stored hash does not verify the given password: ok=%v err=%v", ok, err)
	}

	entries := store.auditEntriesByAction("auth.bootstrap_admin")
	if len(entries) != 1 || entries[0].ActorID != nil || *entries[0].ResourceID != u.ID {
		t.Fatalf("audit entries = %+v", entries)
	}
}

func TestBootstrapAdminIsNoopWhenAdminExists(t *testing.T) {
	store := newFakeStore()
	testAdminActor(store) // pre-existing active admin
	service := newTestService(store)

	created, err := service.BootstrapAdmin(context.Background(), "second-admin", "correct-horse")
	if err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	if created {
		t.Fatal("created = true, want false when an active admin already exists")
	}
	if _, err := store.UserByLogin(context.Background(), fakeTx{}, "second-admin"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UserByLogin(second-admin) error = %v, want ErrNotFound", err)
	}
	if len(store.auditEntriesByAction("auth.bootstrap_admin")) != 0 {
		t.Fatal("audit entry recorded for a no-op bootstrap")
	}
}

func TestBootstrapAdminRejectsInvalidCredentials(t *testing.T) {
	store := newFakeStore()
	service := newTestService(store)

	_, err := service.BootstrapAdmin(context.Background(), "bad login", "correct-horse")
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "login" {
		t.Fatalf("BootstrapAdmin() error = %v, want a login ValidationError", err)
	}
	if len(store.auditEntriesByAction("auth.bootstrap_admin")) != 0 {
		t.Fatal("audit entry recorded for a rejected bootstrap")
	}
}
