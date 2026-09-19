package auth

import (
	"context"

	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateUser validates n, hashes its password, and inserts the new user,
// auditing the effect in the same transaction (CLAUDE.md: "Preserve one
// database transaction where a domain change, audit record ... must be
// atomic"). A login that already exists surfaces as ErrLoginTaken —
// internal/auth/postgres.Store.InsertUser translates the database's own
// unique-constraint violation into it, so this never has to pre-check.
func (s *Service) CreateUser(ctx context.Context, n NewUser, actor Principal, requestID string) (User, error) {
	if err := ValidateNewUser(n); err != nil {
		return User{}, err
	}
	hash, err := HashPassword(n.Password, DefaultParams)
	if err != nil {
		return User{}, ErrStorage
	}
	candidate := User{
		ID: uuid.New(), Login: n.Login, PasswordHash: hash, FullName: n.FullName,
		Role: n.Role, ServiceCode: n.ServiceCode, Level: n.LevelOrDefault(), Active: true,
	}

	var created User
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		u, err := s.store.InsertUser(ctx, tx, candidate)
		if err != nil {
			return err
		}
		created = u
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actor.UserID, ActorRole: string(actor.Role), Action: "admin.user.create",
			ResourceType: "user", ResourceID: &created.ID, Outcome: audit.OutcomeOK, RequestID: requestID,
		})
	})
	if err != nil {
		return User{}, err
	}
	return created, nil
}

// UpdateUser applies patch to the user id, refusing a patch that would
// leave zero active admins (ErrLastAdmin) and invalidating every one of
// the user's sessions when the patch changes the password or deactivates
// the account — a stolen or now-wrong session must stop working
// immediately, not linger until its natural expiry.
func (s *Service) UpdateUser(ctx context.Context, id uuid.UUID, patch Patch, actor Principal, requestID string) (User, error) {
	var updated User
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		current, err := s.store.UserByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := ValidateUserPatch(current, patch); err != nil {
			return err
		}
		if wouldLoseLastActiveAdmin(current, patch) {
			count, err := s.store.CountActiveAdmins(ctx, tx)
			if err != nil {
				return err
			}
			if count <= 1 {
				return ErrLastAdmin
			}
		}

		storeUpdate, err := buildUserUpdate(patch)
		if err != nil {
			return err
		}
		u, err := s.store.UpdateUser(ctx, tx, id, storeUpdate)
		if err != nil {
			return err
		}
		updated = u

		if err := s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actor.UserID, ActorRole: string(actor.Role), Action: "admin.user.update",
			ResourceType: "user", ResourceID: &id, Outcome: audit.OutcomeOK, RequestID: requestID,
		}); err != nil {
			return err
		}

		deactivated := patch.Active != nil && !*patch.Active
		if patch.Password != nil || deactivated {
			return s.store.DeleteUserSessions(ctx, tx, id)
		}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return updated, nil
}

// BootstrapAdmin creates the initial admin account for an empty
// installation (slice-planning.md §2: "первоначальная учётная запись
// администратора для пустой установки"; cmd/emsim's "bootstrap-admin"
// subcommand). It is idempotent by design rather than by retry: if an
// active admin already exists, it changes nothing and reports
// created=false, so a compose one-shot service can run it on every
// startup without ever creating a second admin or touching the first
// one. There is no authenticated actor before the very first admin
// exists, so the audit row carries a nil ActorID.
func (s *Service) BootstrapAdmin(ctx context.Context, login, password string) (bool, error) {
	n := NewUser{Login: login, Password: password, FullName: "Administrator", Role: RoleAdmin, Level: LevelEasy}
	if err := ValidateNewUser(n); err != nil {
		return false, err
	}
	hash, err := HashPassword(n.Password, DefaultParams)
	if err != nil {
		return false, ErrStorage
	}
	candidate := User{
		ID: uuid.New(), Login: n.Login, PasswordHash: hash, FullName: n.FullName,
		Role: n.Role, Level: n.LevelOrDefault(), Active: true,
	}

	var created bool
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		count, err := s.store.CountActiveAdmins(ctx, tx)
		if err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		u, err := s.store.InsertUser(ctx, tx, candidate)
		if err != nil {
			return err
		}
		created = true
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorRole: "system", Action: "auth.bootstrap_admin",
			ResourceType: "user", ResourceID: &u.ID, Outcome: audit.OutcomeOK,
		})
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

// wouldLoseLastActiveAdmin reports whether patch, applied to current,
// would leave current no longer counted as an active admin — the trigger
// to check CountActiveAdmins at all. A user who is not currently an active
// admin can never "lose" that status by definition, so this is false for
// them regardless of what the patch says.
func wouldLoseLastActiveAdmin(current User, patch Patch) bool {
	if current.Role != RoleAdmin || !current.Active {
		return false
	}
	stillAdmin := current.Role == RoleAdmin
	if patch.Role != nil {
		stillAdmin = *patch.Role == RoleAdmin
	}
	stillActive := current.Active
	if patch.Active != nil {
		stillActive = *patch.Active
	}
	return !(stillAdmin && stillActive)
}

// buildUserUpdate lowers a Patch into the store's UserUpdate: it hashes a
// new password (the store never sees a plaintext one) and resolves
// ServiceCode's empty-string-means-clear convention into the store's
// explicit ServiceCodeSet/nil-means-NULL one.
func buildUserUpdate(patch Patch) (UserUpdate, error) {
	update := UserUpdate{FullName: patch.FullName, Role: patch.Role, Level: patch.Level, Active: patch.Active}
	if patch.Password != nil {
		hash, err := HashPassword(*patch.Password, DefaultParams)
		if err != nil {
			return UserUpdate{}, ErrStorage
		}
		update.PasswordHash = &hash
		update.PasswordHashSet = true
	}
	if patch.ServiceCode != nil {
		update.ServiceCodeSet = true
		if *patch.ServiceCode != "" {
			update.ServiceCode = patch.ServiceCode
		}
	}
	return update, nil
}

// ListUsers returns page (1-based) of pageSize users ordered by login,
// with the total row count (openapi.yaml GET /admin/users).
func (s *Service) ListUsers(ctx context.Context, page, pageSize int) ([]User, int, error) {
	var users []User
	var total int
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		u, t, err := s.store.ListUsers(ctx, tx, page, pageSize)
		if err != nil {
			return err
		}
		users, total = u, t
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// ListWorkstations returns every workstation ordered by number, active or
// not (openapi.yaml GET /admin/workstations).
func (s *Service) ListWorkstations(ctx context.Context) ([]Workstation, error) {
	var workstations []Workstation
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		w, err := s.store.ListWorkstations(ctx, tx)
		workstations = w
		return err
	})
	return workstations, err
}

// ReplaceWorkstations implements PUT /admin/workstations' "заменить
// список" semantics: every workstation in the body is created or updated
// (matched by Number, reactivated if it existed and was inactive), and
// every workstation NOT in the body is deactivated — never deleted, so
// history (assignments, sessions) referencing it stays intact. It returns
// the full resulting list, not just what the caller sent, since a
// deactivated row is part of what changed too.
func (s *Service) ReplaceWorkstations(ctx context.Context, workstations []Workstation, actor Principal, requestID string) ([]Workstation, error) {
	if err := validateWorkstationReplace(workstations); err != nil {
		return nil, err
	}

	var result []Workstation
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.store.UpsertWorkstations(ctx, tx, workstations); err != nil {
			return err
		}
		numbers := make([]int, len(workstations))
		for i, w := range workstations {
			numbers[i] = w.Number
		}
		if err := s.store.DeactivateWorkstationsNotIn(ctx, tx, numbers); err != nil {
			return err
		}
		all, err := s.store.ListWorkstations(ctx, tx)
		if err != nil {
			return err
		}
		result = all
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &actor.UserID, ActorRole: string(actor.Role), Action: "admin.workstations.replace",
			ResourceType: "workstation", Outcome: audit.OutcomeOK, RequestID: requestID,
			Details: map[string]any{"count": len(workstations)},
		})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// validateWorkstationReplace rejects a request body openapi.yaml's schema
// alone does not rule out: a non-positive number (schema says minimum: 1,
// but this defends the same rule at this layer too) and a number repeated
// more than once, which "заменить список по number" cannot make sense of.
func validateWorkstationReplace(workstations []Workstation) error {
	seen := make(map[int]struct{}, len(workstations))
	for _, w := range workstations {
		if w.Number < 1 {
			return invalid("number", "invalid")
		}
		if _, duplicate := seen[w.Number]; duplicate {
			return invalid("number", "duplicate")
		}
		seen[w.Number] = struct{}{}
	}
	return nil
}
