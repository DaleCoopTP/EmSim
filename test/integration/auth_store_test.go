// New test: internal/auth/postgres.Store against real PostgreSQL —
// login uniqueness (23505 -> auth.ErrLoginTaken), session expiry compared
// against PostgreSQL's own clock_timestamp() rather than a Go-side
// deadline, the sliding renewal TouchSession implements, workstation
// upsert/deactivate ("заменить список" semantics), and that a session and
// its audit_log row commit or roll back together through Store.WithTx.
//
//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"
	"emsim/internal/platform/audit"
	pgstore "emsim/internal/platform/postgres"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newTrainee(login, serviceCode string) auth.User {
	code := serviceCode
	return auth.User{
		ID:           uuid.New(),
		Login:        login,
		PasswordHash: "$argon2id$v=19$m=8192,t=1,p=1$c2FsdHNhbHQ$aGFzaGhhc2g",
		FullName:     "Иванов Иван Иванович",
		Role:         auth.RoleTrainee,
		ServiceCode:  &code,
		Level:        auth.LevelEasy,
		Active:       true,
	}
}

func newAdmin(login string) auth.User {
	u := newTrainee(login, "")
	u.Role = auth.RoleAdmin
	u.ServiceCode = nil
	return u
}

// insertService satisfies users_service_code_fkey (migrations/00004) for
// tests that insert a trainee with a given service_code. It writes
// directly with SQL rather than through internal/content (content_import_
// test.go exercises that module's own store/import) — a bare
// content.services row is all auth's own tests need from the FK's far side.
func insertService(t *testing.T, ctx context.Context, pool *pgxpool.Pool, code string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO services (code, name, workflow) VALUES ($1, $1, '{}'::jsonb)`, code); err != nil {
		t.Fatalf("insert fixture service %q: %v", code, err)
	}
}

func withTx(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		t.Fatalf("tx body: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tx: %v", err)
	}
}

func TestAuthStoreInsertUserRejectsDuplicateLogin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	first := newAdmin("dispatcher-dup")
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := store.InsertUser(ctx, tx, first)
		return err
	})

	second := newAdmin("dispatcher-dup")
	err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := store.InsertUser(ctx, tx, second)
		return err
	})
	if !errors.Is(err, auth.ErrLoginTaken) {
		t.Fatalf("InsertUser() duplicate login error = %v, want auth.ErrLoginTaken", err)
	}
}

// TestAuthStoreInsertUserConcurrentDuplicateLoginOnlyOneSucceeds exercises
// the same guarantee as the sequential test above, but under a real race:
// many goroutines racing to insert the same login concurrently must still
// leave exactly one winner, enforced by the users_login_key unique index
// itself, not by anything sequential in Go. C6's admin.user.create path
// (Service.CreateUser) relies on this — the service never pre-checks
// login availability, it lets the database decide.
func TestAuthStoreInsertUserConcurrentDuplicateLoginOnlyOneSucceeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	const attempts = 10
	var wg sync.WaitGroup
	var successCount, conflictCount, otherErrCount int32

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := store.WithTx(ctx, func(tx pgx.Tx) error {
				_, err := store.InsertUser(ctx, tx, newAdmin("dispatcher-race"))
				return err
			})
			switch {
			case err == nil:
				atomic.AddInt32(&successCount, 1)
			case errors.Is(err, auth.ErrLoginTaken):
				atomic.AddInt32(&conflictCount, 1)
			default:
				atomic.AddInt32(&otherErrCount, 1)
				t.Errorf("unexpected InsertUser() error: %v", err)
			}
		}()
	}
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("successCount = %d, want exactly 1", successCount)
	}
	if conflictCount != attempts-1 {
		t.Fatalf("conflictCount = %d, want %d", conflictCount, attempts-1)
	}
	if otherErrCount != 0 {
		t.Fatalf("otherErrCount = %d, want 0", otherErrCount)
	}

	var rowCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE login = 'dispatcher-race'`).Scan(&rowCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("users with login dispatcher-race = %d, want 1", rowCount)
	}
}

func TestAuthStoreInsertUserAssignsCreatedAtFromPostgresClock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	before := time.Now().Add(-time.Second)
	var inserted auth.User
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		var err error
		inserted, err = store.InsertUser(ctx, tx, newAdmin("dispatcher-clock"))
		return err
	})
	if inserted.CreatedAt.Before(before) {
		t.Fatalf("CreatedAt = %v, want a server-assigned time at or after %v", inserted.CreatedAt, before)
	}
}

func TestAuthStoreUserByLoginAndByIDRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)
	insertService(t, ctx, pool, "dds_district")

	want := newTrainee("dispatcher-lookup", "dds_district")
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		inserted, err := store.InsertUser(ctx, tx, want)
		want = inserted
		return err
	})

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		byLogin, err := store.UserByLogin(ctx, tx, "dispatcher-lookup")
		if err != nil {
			t.Fatalf("UserByLogin() error = %v", err)
		}
		assertUsersEqual(t, byLogin, want)
		byID, err := store.UserByID(ctx, tx, want.ID)
		if err != nil {
			t.Fatalf("UserByID() error = %v", err)
		}
		assertUsersEqual(t, byID, want)
		return nil
	})
}

// assertUsersEqual compares every field, dereferencing ServiceCode by
// value — auth.User == auth.User compares ServiceCode by pointer
// identity, which two separately-scanned rows never share even when they
// hold the same string.
func assertUsersEqual(t *testing.T, got, want auth.User) {
	t.Helper()
	if got.ID != want.ID || got.Login != want.Login || got.PasswordHash != want.PasswordHash ||
		got.FullName != want.FullName || got.Role != want.Role || got.Level != want.Level ||
		got.Active != want.Active || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("user = %+v, want %+v", got, want)
	}
	switch {
	case got.ServiceCode == nil && want.ServiceCode == nil:
	case got.ServiceCode == nil || want.ServiceCode == nil:
		t.Fatalf("ServiceCode = %v, want %v", got.ServiceCode, want.ServiceCode)
	case *got.ServiceCode != *want.ServiceCode:
		t.Fatalf("ServiceCode = %q, want %q", *got.ServiceCode, *want.ServiceCode)
	}
}

func TestAuthStoreUserByLoginNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := store.UserByLogin(ctx, tx, "does-not-exist")
		if !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("UserByLogin() error = %v, want auth.ErrNotFound", err)
		}
		return nil
	})
}

func TestAuthStoreListUsersOrderedByLoginWithTotal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	logins := []string{"c-user", "a-user", "b-user"}
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		for _, login := range logins {
			if _, err := store.InsertUser(ctx, tx, newAdmin(login)); err != nil {
				return err
			}
		}
		return nil
	})

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		page, total, err := store.ListUsers(ctx, tx, 1, 2)
		if err != nil {
			t.Fatalf("ListUsers() error = %v", err)
		}
		if total != 3 {
			t.Fatalf("total = %d, want 3", total)
		}
		if len(page) != 2 || page[0].Login != "a-user" || page[1].Login != "b-user" {
			t.Fatalf("page 1 = %v, want [a-user b-user]", loginsOf(page))
		}
		page2, _, err := store.ListUsers(ctx, tx, 2, 2)
		if err != nil {
			t.Fatalf("ListUsers() page 2 error = %v", err)
		}
		if len(page2) != 1 || page2[0].Login != "c-user" {
			t.Fatalf("page 2 = %v, want [c-user]", loginsOf(page2))
		}
		return nil
	})
}

func loginsOf(users []auth.User) []string {
	logins := make([]string, len(users))
	for i, u := range users {
		logins[i] = u.Login
	}
	return logins
}

func TestAuthStoreUpdateUserPartialUpdateLeavesOtherFieldsAlone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)
	insertService(t, ctx, pool, "dds_district")

	var original auth.User
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		var err error
		original, err = store.InsertUser(ctx, tx, newTrainee("dispatcher-patch", "dds_district"))
		return err
	})

	newName := "Петров Пётр Петрович"
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		updated, err := store.UpdateUser(ctx, tx, original.ID, auth.UserUpdate{FullName: &newName})
		if err != nil {
			t.Fatalf("UpdateUser() error = %v", err)
		}
		if updated.FullName != newName {
			t.Fatalf("FullName = %q, want %q", updated.FullName, newName)
		}
		if updated.Login != original.Login || updated.Role != original.Role || *updated.ServiceCode != *original.ServiceCode {
			t.Fatalf("untouched fields changed: got %+v, from %+v", updated, original)
		}
		return nil
	})
}

func TestAuthStoreUpdateUserEmptyUpdateReturnsCurrentRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	var original auth.User
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		var err error
		original, err = store.InsertUser(ctx, tx, newAdmin("dispatcher-noop"))
		return err
	})

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		got, err := store.UpdateUser(ctx, tx, original.ID, auth.UserUpdate{})
		if err != nil {
			t.Fatalf("UpdateUser(empty) error = %v", err)
		}
		assertUsersEqual(t, got, original)
		return nil
	})
}

func TestAuthStoreUpdateUserClearsServiceCode(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)
	insertService(t, ctx, pool, "dds_district")

	var original auth.User
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		var err error
		original, err = store.InsertUser(ctx, tx, newTrainee("dispatcher-clear", "dds_district"))
		return err
	})

	newRole := auth.RoleInstructor
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		// At the store layer, ServiceCode == nil with ServiceCodeSet ==
		// true means "set to NULL" — the empty-string-means-clear
		// convention belongs to auth.Patch (domain.go), which service.go
		// translates into this before calling UpdateUser.
		updated, err := store.UpdateUser(ctx, tx, original.ID, auth.UserUpdate{
			Role: &newRole, ServiceCode: nil, ServiceCodeSet: true,
		})
		if err != nil {
			t.Fatalf("UpdateUser() error = %v", err)
		}
		if updated.ServiceCode != nil {
			t.Fatalf("ServiceCode = %v, want nil after clearing", *updated.ServiceCode)
		}
		if updated.Role != auth.RoleInstructor {
			t.Fatalf("Role = %s, want instructor", updated.Role)
		}
		return nil
	})
}

func TestAuthServiceUpdateUserServiceLookupWithSingleConnection(t *testing.T) {
	setupCtx, setupCancel := context.WithTimeout(context.Background(), time.Minute)
	defer setupCancel()
	databaseURL := openTestDatabase(t, setupCtx)
	if err := pgstore.Up(setupCtx, databaseURL); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(setupCtx, config)
	if err != nil {
		t.Fatalf("open single-connection pool: %v", err)
	}
	defer pool.Close()

	insertService(t, setupCtx, pool, "svc_old")
	insertService(t, setupCtx, pool, "svc_new")
	authStore := authpg.NewStore(pool)
	var actor, target auth.User
	if err := authStore.WithTx(setupCtx, func(tx pgx.Tx) error {
		var err error
		actor, err = authStore.InsertUser(setupCtx, tx, newAdmin("single-pool-admin"))
		if err != nil {
			return err
		}
		target, err = authStore.InsertUser(setupCtx, tx, newTrainee("single-pool-trainee", "svc_old"))
		return err
	}); err != nil {
		t.Fatalf("insert users: %v", err)
	}

	catalog := content.NewService(contentpg.NewStore(pool), nil)
	service := auth.NewService(authStore, nil, time.Hour, nil, catalog)
	newCode := "svc_new"
	updateCtx, updateCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer updateCancel()
	updated, err := service.UpdateUser(updateCtx, target.ID, auth.Patch{ServiceCode: &newCode}, auth.Principal{
		UserID: actor.ID,
		Role:   actor.Role,
	}, "single-pool-update")
	if err != nil {
		t.Fatalf("UpdateUser() with MaxConns=1 error = %v", err)
	}
	if updated.ServiceCode == nil || *updated.ServiceCode != newCode {
		t.Fatalf("updated.ServiceCode = %v, want %q", updated.ServiceCode, newCode)
	}
}

func TestAuthStoreCountActiveAdmins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)
	insertService(t, ctx, pool, "dds_district")

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		count, err := store.CountActiveAdmins(ctx, tx)
		if err != nil {
			return err
		}
		if count != 0 {
			t.Fatalf("initial CountActiveAdmins() = %d, want 0", count)
		}
		if _, err := store.InsertUser(ctx, tx, newAdmin("admin-1")); err != nil {
			return err
		}
		inactiveAdmin := newAdmin("admin-2")
		inactiveAdmin.Active = false
		if _, err := store.InsertUser(ctx, tx, inactiveAdmin); err != nil {
			return err
		}
		if _, err := store.InsertUser(ctx, tx, newTrainee("trainee-1", "dds_district")); err != nil {
			return err
		}
		count, err = store.CountActiveAdmins(ctx, tx)
		if err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("CountActiveAdmins() = %d, want 1 (one active admin, one inactive, one trainee)", count)
		}
		return nil
	})
}

func TestAuthStoreWorkstationUpsertAndDeactivateNotIn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		upserted, err := store.UpsertWorkstations(ctx, tx, []auth.Workstation{
			{Number: 1, Label: "РМ-01"},
			{Number: 2, Label: "РМ-02"},
		})
		if err != nil {
			return err
		}
		if len(upserted) != 2 || !upserted[0].Active || !upserted[1].Active {
			t.Fatalf("initial upsert = %+v, want both active", upserted)
		}
		return nil
	})

	// "Replace" with just number 1: number 2 must be deactivated, not deleted.
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		if _, err := store.UpsertWorkstations(ctx, tx, []auth.Workstation{{Number: 1, Label: "РМ-01 renamed"}}); err != nil {
			return err
		}
		return store.DeactivateWorkstationsNotIn(ctx, tx, []int{1})
	})

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		all, err := store.ListWorkstations(ctx, tx)
		if err != nil {
			return err
		}
		if len(all) != 2 {
			t.Fatalf("ListWorkstations() = %v, want 2 rows still present (deactivated, not deleted)", all)
		}
		if !all[0].Active || all[0].Label != "РМ-01 renamed" {
			t.Fatalf("workstation 1 = %+v, want active and renamed", all[0])
		}
		if all[1].Active {
			t.Fatalf("workstation 2 = %+v, want deactivated", all[1])
		}
		return nil
	})

	// Re-including number 2 reactivates it.
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		if _, err := store.UpsertWorkstations(ctx, tx, []auth.Workstation{{Number: 2, Label: "РМ-02"}}); err != nil {
			return err
		}
		reactivated, err := store.WorkstationByNumber(ctx, tx, 2)
		if err != nil {
			return err
		}
		if !reactivated.Active {
			t.Fatal("workstation 2 was not reactivated by a fresh upsert")
		}
		return nil
	})
}

func TestAuthStoreDeactivateWorkstationsNotInWithEmptyListDeactivatesAll(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := store.UpsertWorkstations(ctx, tx, []auth.Workstation{{Number: 5, Label: "РМ-05"}})
		return err
	})
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		return store.DeactivateWorkstationsNotIn(ctx, tx, nil)
	})
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		w, err := store.WorkstationByNumber(ctx, tx, 5)
		if err != nil {
			return err
		}
		if w.Active {
			t.Fatal("DeactivateWorkstationsNotIn(nil) left a workstation active, want all deactivated")
		}
		return nil
	})
}

func TestAuthStoreSessionExpiresByPostgresClockNotGoDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	var user auth.User
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		var err error
		user, err = store.InsertUser(ctx, tx, newAdmin("dispatcher-expiry"))
		return err
	})

	sessionID := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		// Negative TTL: already expired the instant it is inserted,
		// decided entirely by PostgreSQL's clock_timestamp(), not by any
		// Go-side time.Now() the test controls.
		_, err := store.InsertSession(ctx, tx, auth.Session{ID: sessionID, UserID: user.ID}, -time.Second)
		return err
	})

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := store.SessionByID(ctx, tx, sessionID)
		if !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("SessionByID() on an expired session = %v, want auth.ErrNotFound", err)
		}
		return nil
	})
}

func TestAuthStoreSessionByIDJoinsUserAndWorkstation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)
	insertService(t, ctx, pool, "dds_district")

	var user auth.User
	var workstation auth.Workstation
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		var err error
		user, err = store.InsertUser(ctx, tx, newTrainee("dispatcher-session", "dds_district"))
		if err != nil {
			return err
		}
		ws, err := store.UpsertWorkstations(ctx, tx, []auth.Workstation{{Number: 7, Label: "РМ-07"}})
		if err != nil {
			return err
		}
		workstation = ws[0]
		return nil
	})

	sessionID := []byte("workstation-session-id-32-bytes!")
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		wsID := workstation.ID
		_, err := store.InsertSession(ctx, tx, auth.Session{ID: sessionID, UserID: user.ID, WorkstationID: &wsID}, time.Hour)
		return err
	})

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		lookup, err := store.SessionByID(ctx, tx, sessionID)
		if err != nil {
			t.Fatalf("SessionByID() error = %v", err)
		}
		if lookup.User.ID != user.ID || lookup.User.Login != user.Login {
			t.Fatalf("lookup.User = %+v, want %+v", lookup.User, user)
		}
		if lookup.Workstation == nil || lookup.Workstation.ID != workstation.ID {
			t.Fatalf("lookup.Workstation = %+v, want %+v", lookup.Workstation, workstation)
		}
		return nil
	})

	// A session with no workstation must join to a nil Workstation, not a
	// zero-valued one.
	noWorkstationSessionID := []byte("no-workstation-session-32-bytes!")
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := store.InsertSession(ctx, tx, auth.Session{ID: noWorkstationSessionID, UserID: user.ID}, time.Hour)
		return err
	})
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		lookup, err := store.SessionByID(ctx, tx, noWorkstationSessionID)
		if err != nil {
			t.Fatalf("SessionByID() error = %v", err)
		}
		if lookup.Workstation != nil {
			t.Fatalf("lookup.Workstation = %+v, want nil", lookup.Workstation)
		}
		return nil
	})
}

func TestAuthStoreTouchSessionSlidingRenewal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	var user auth.User
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		var err error
		user, err = store.InsertUser(ctx, tx, newAdmin("dispatcher-touch"))
		return err
	})

	sessionID := []byte("touch-session-id-exactly-32bytes")
	var initialExpiry time.Time
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		session, err := store.InsertSession(ctx, tx, auth.Session{ID: sessionID, UserID: user.ID}, time.Hour)
		initialExpiry = session.ExpiresAt
		return err
	})

	// A fresh session (last_seen_at just now) is within the staleAfter
	// window: TouchSession must be a no-op.
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		return store.TouchSession(ctx, tx, sessionID, 5*time.Minute, time.Hour)
	})
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		lookup, err := store.SessionByID(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if !lookup.Session.ExpiresAt.Equal(initialExpiry) {
			t.Fatalf("ExpiresAt changed on a fresh session: got %v, want unchanged %v", lookup.Session.ExpiresAt, initialExpiry)
		}
		return nil
	})

	// Backdate last_seen_at past the stale threshold directly, then touch
	// again: this time it must renew.
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE sessions SET last_seen_at = clock_timestamp() - interval '10 minutes' WHERE id = $1`, sessionID)
		return err
	})
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		return store.TouchSession(ctx, tx, sessionID, 5*time.Minute, time.Hour)
	})
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		lookup, err := store.SessionByID(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if !lookup.Session.ExpiresAt.After(initialExpiry) {
			t.Fatalf("ExpiresAt = %v, want it renewed past %v", lookup.Session.ExpiresAt, initialExpiry)
		}
		if time.Since(lookup.Session.LastSeenAt) > time.Minute {
			t.Fatalf("LastSeenAt = %v, want close to now", lookup.Session.LastSeenAt)
		}
		return nil
	})
}

func TestAuthStoreDeleteSessionAndDeleteUserSessions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	var user auth.User
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		var err error
		user, err = store.InsertUser(ctx, tx, newAdmin("dispatcher-delete"))
		return err
	})

	sessionA := []byte("delete-session-a-id-32-bytes-aaa")
	sessionB := []byte("delete-session-b-id-32-bytes-bbb")
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		for _, id := range [][]byte{sessionA, sessionB} {
			if _, err := store.InsertSession(ctx, tx, auth.Session{ID: id, UserID: user.ID}, time.Hour); err != nil {
				return err
			}
		}
		return nil
	})

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		return store.DeleteSession(ctx, tx, sessionA)
	})
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		if _, err := store.SessionByID(ctx, tx, sessionA); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("SessionByID(sessionA) after DeleteSession = %v, want auth.ErrNotFound", err)
		}
		if _, err := store.SessionByID(ctx, tx, sessionB); err != nil {
			t.Fatalf("SessionByID(sessionB) = %v, want it untouched by deleting sessionA", err)
		}
		return nil
	})

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		return store.DeleteUserSessions(ctx, tx, user.ID)
	})
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		if _, err := store.SessionByID(ctx, tx, sessionB); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("SessionByID(sessionB) after DeleteUserSessions = %v, want auth.ErrNotFound", err)
		}
		return nil
	})
}

func TestAuthStoreWithTxCommitsSessionAndAuditTogether(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	var user auth.User
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		var err error
		user, err = store.InsertUser(ctx, tx, newAdmin("dispatcher-atomic"))
		return err
	})

	sessionID := []byte("atomic-commit-session-32-bytes!!")
	err := store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := store.InsertSession(ctx, tx, auth.Session{ID: sessionID, UserID: user.ID}, time.Hour); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{
			ActorID: &user.ID, ActorRole: "admin", Action: "auth.login",
			ResourceType: "user", ResourceID: &user.ID, Outcome: audit.OutcomeOK, RequestID: "req-atomic-commit",
		})
	})
	if err != nil {
		t.Fatalf("WithTx() error = %v", err)
	}

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		if _, err := store.SessionByID(ctx, tx, sessionID); err != nil {
			t.Fatalf("session missing after successful WithTx: %v", err)
		}
		var auditCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE request_id = 'req-atomic-commit'`).Scan(&auditCount); err != nil {
			return err
		}
		if auditCount != 1 {
			t.Fatalf("audit rows for the committed WithTx = %d, want 1", auditCount)
		}
		return nil
	})
}

func TestAuthStoreWithTxRollsBackSessionAndAuditTogether(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := migratedTestPool(t, ctx)
	store := authpg.NewStore(pool)

	var user auth.User
	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		var err error
		user, err = store.InsertUser(ctx, tx, newAdmin("dispatcher-atomic-rollback"))
		return err
	})

	failure := errors.New("boom")
	sessionID := []byte("atomic-rollback-session-32bytes!")
	err := store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := store.InsertSession(ctx, tx, auth.Session{ID: sessionID, UserID: user.ID}, time.Hour); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{
			ActorID: &user.ID, ActorRole: "admin", Action: "auth.login",
			ResourceType: "user", ResourceID: &user.ID, Outcome: audit.OutcomeOK, RequestID: "req-atomic-rollback",
		}); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("WithTx() error = %v, want the fn's own error", err)
	}

	withTx(t, ctx, pool, func(tx pgx.Tx) error {
		if _, err := store.SessionByID(ctx, tx, sessionID); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("session present after a rolled-back WithTx: err = %v, want auth.ErrNotFound", err)
		}
		var auditCount int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE request_id = 'req-atomic-rollback'`).Scan(&auditCount); err != nil {
			return err
		}
		if auditCount != 0 {
			t.Fatalf("audit rows for the rolled-back WithTx = %d, want 0", auditCount)
		}
		return nil
	})
}
