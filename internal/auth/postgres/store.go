// Package postgres is the pgx-backed adapter for the auth module's ports
// (CLAUDE.md: "HTTP, PostgreSQL, files, STT, and LLM are adapters" — the
// narrow Store interface Service actually needs is declared in
// internal/auth/service.go, its consumer; this package holds the concrete
// implementation and every method any consumer needs, structurally
// satisfying that interface without either package naming the other's
// interface type). Errors this package cannot map to a specific domain
// sentinel (auth.ErrLoginTaken, say) come back as auth.ErrNotFound or
// auth.ErrStorage — both declared in internal/auth, not here, precisely so
// service.go can check for them without importing its own adapter.
//
// Every method except WithTx takes an explicit pgx.Tx instead of opening
// its own transaction (CLAUDE.md: "Use pgx.Tx for atomic domain and queue
// operations. The caller owns commit and rollback.") — WithTx is the one
// place that owns commit/rollback, so a caller that needs a domain change,
// its audit_log row, and (from later slices) a related tasks.EnqueueTx
// atomic in one commit gets there by running all of them inside one
// WithTx closure, passing the same tx to each Store/audit call.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"emsim/internal/auth"
	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// *Store structurally satisfies auth.Store (its consumer's own interface,
// internal/auth/service.go) — asserted here so a divergence between the
// two fails the build in this package, at the adapter, rather than as a
// confusing error where service.go is composed (cmd/emsim/api.go).
var _ auth.Store = (*Store)(nil)

// WithTx runs fn inside a fresh READ COMMITTED transaction (RFC-001 §8),
// committing only if fn returns nil and rolling back otherwise — including
// on panic, since the deferred Rollback always runs and a Commit after a
// panic never happens. It is the only way a caller opens a transaction
// against this store.
func (s *Store) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return auth.ErrStorage
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.ErrStorage
	}
	return nil
}

// ------------------------------------------------------------ users

const userColumns = `id, login, password_hash, full_name, role, service_code, level, active, created_at`

func scanUser(row pgx.Row) (auth.User, error) {
	var u auth.User
	err := row.Scan(&u.ID, &u.Login, &u.PasswordHash, &u.FullName, &u.Role, &u.ServiceCode, &u.Level, &u.Active, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.User{}, auth.ErrStorage
	}
	return u, nil
}

func (s *Store) UserByLogin(ctx context.Context, tx pgx.Tx, login string) (auth.User, error) {
	return scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE login = $1`, login))
}

func (s *Store) UserByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (auth.User, error) {
	return scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

// ListUsers returns page (1-based) of pageSize users ordered by login,
// alongside the total row count. page/pageSize below 1 are treated as 1/20.
func (s *Store) ListUsers(ctx context.Context, tx pgx.Tx, page, pageSize int) ([]auth.User, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, auth.ErrStorage
	}

	rows, err := tx.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY login LIMIT $1 OFFSET $2`,
		pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, auth.ErrStorage
	}
	defer rows.Close()

	users := make([]auth.User, 0, pageSize)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, auth.ErrStorage
	}
	return users, total, nil
}

// InsertUser persists u (u.ID is the caller's to generate, matching
// tasks.EnqueueRequest's own caller-supplied id — see
// internal/platform/tasks/queue.go). u.CreatedAt is ignored on input:
// PostgreSQL's own clock assigns it (column DEFAULT now()), and the
// returned User carries the actual persisted value. A login that already
// exists surfaces as auth.ErrLoginTaken — the adapter translates the
// unique-constraint violation into the domain error here, so service.go
// never has to inspect a *pgconn.PgError itself.
func (s *Store) InsertUser(ctx context.Context, tx pgx.Tx, u auth.User) (auth.User, error) {
	err := tx.QueryRow(ctx, `
		INSERT INTO users (id, login, password_hash, full_name, role, service_code, level, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at
	`, u.ID, u.Login, u.PasswordHash, u.FullName, string(u.Role), u.ServiceCode, string(u.Level), u.Active,
	).Scan(&u.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return auth.User{}, auth.ErrLoginTaken
		}
		return auth.User{}, auth.ErrStorage
	}
	return u, nil
}

// UserUpdate is the storage-layer partial update for one user — lower-
// level than auth.Patch: PasswordHash is already hashed (service.go calls
// auth.HashPassword on auth.Patch.Password before building this; the store
// never sees a plaintext password), and login is absent because it is
// immutable after creation (openapi.yaml UserPatch has no login field).
// Each field pairs a value with its own "touched" flag instead of relying
// on a nil pointer to mean "leave unchanged", so ServiceCode can be set to
// NULL without a double pointer.
type UserUpdate struct {
	PasswordHash    *string
	PasswordHashSet bool
	FullName        *string
	Role            *auth.Role
	ServiceCode     *string
	ServiceCodeSet  bool
	Level           *auth.Level
	Active          *bool
}

// UpdateUser applies update to the user identified by id and returns the
// resulting row. An update touching no fields is a no-op read — it still
// returns the current row, so a caller building an update from an
// all-nil-fields Patch does not need to special-case "nothing to do".
func (s *Store) UpdateUser(ctx context.Context, tx pgx.Tx, id uuid.UUID, update UserUpdate) (auth.User, error) {
	var sets []string
	var args []any
	add := func(column string, value any) {
		args = append(args, value)
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if update.PasswordHashSet {
		add("password_hash", update.PasswordHash)
	}
	if update.FullName != nil {
		add("full_name", *update.FullName)
	}
	if update.Role != nil {
		add("role", string(*update.Role))
	}
	if update.ServiceCodeSet {
		add("service_code", update.ServiceCode)
	}
	if update.Level != nil {
		add("level", string(*update.Level))
	}
	if update.Active != nil {
		add("active", *update.Active)
	}

	if len(sets) == 0 {
		return s.UserByID(ctx, tx, id)
	}
	args = append(args, id)
	query := `UPDATE users SET ` + strings.Join(sets, ", ") + fmt.Sprintf(" WHERE id = $%d RETURNING %s", len(args), userColumns)
	return scanUser(tx.QueryRow(ctx, query, args...))
}

// CountActiveAdmins returns how many users are both role=admin and active
// — the guard against demoting or deactivating the last one (auth.ErrLastAdmin)
// is service.go's job (C6); this only supplies the count.
func (s *Store) CountActiveAdmins(ctx context.Context, tx pgx.Tx) (int, error) {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND active`).Scan(&count); err != nil {
		return 0, auth.ErrStorage
	}
	return count, nil
}

// ------------------------------------------------------------ workstations

// ip_address is cast to text: pgx's inet codec only scans into
// net/netip types (NetipPrefixScanner), not a plain *string, and
// Workstation.IPAddress is a *string (RFC-001 §4.5/openapi.yaml
// Workstation.ip_address: {type: string}) since slice 1 stores the value
// without using it — see IPAddress's doc comment in domain.go.
const workstationColumns = `id, number, label, ip_address::text, active`

func scanWorkstation(row pgx.Row) (auth.Workstation, error) {
	var w auth.Workstation
	err := row.Scan(&w.ID, &w.Number, &w.Label, &w.IPAddress, &w.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Workstation{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.Workstation{}, auth.ErrStorage
	}
	return w, nil
}

func (s *Store) WorkstationByNumber(ctx context.Context, tx pgx.Tx, number int) (auth.Workstation, error) {
	return scanWorkstation(tx.QueryRow(ctx, `SELECT `+workstationColumns+` FROM workstations WHERE number = $1`, number))
}

// WorkstationByID looks up the workstation a Principal (built from
// Service.Authenticate's session join) names by id — used by Service.Me
// to render the full Workstation for GET /me, since Principal only keeps
// the id.
func (s *Store) WorkstationByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (auth.Workstation, error) {
	return scanWorkstation(tx.QueryRow(ctx, `SELECT `+workstationColumns+` FROM workstations WHERE id = $1`, id))
}

func (s *Store) ListWorkstations(ctx context.Context, tx pgx.Tx) ([]auth.Workstation, error) {
	rows, err := tx.Query(ctx, `SELECT `+workstationColumns+` FROM workstations ORDER BY number`)
	if err != nil {
		return nil, auth.ErrStorage
	}
	defer rows.Close()

	var workstations []auth.Workstation
	for rows.Next() {
		w, err := scanWorkstation(rows)
		if err != nil {
			return nil, err
		}
		workstations = append(workstations, w)
	}
	if err := rows.Err(); err != nil {
		return nil, auth.ErrStorage
	}
	return workstations, nil
}

// UpsertWorkstations creates or updates one row per element of
// workstations, matched by Number — the RFC-001 §5 "заменить список"
// semantics for PUT /admin/workstations (slice-planning.md §2 decision:
// upsert by number, not a destructive replace). Any ID on an input element
// is ignored: a new row gets a freshly generated id, and ON CONFLICT
// leaves an existing row's id untouched, so the freshly generated id for
// an existing number is simply discarded by the conflict. Every upserted
// row is (re)activated — pair this with DeactivateWorkstationsNotIn to
// deactivate what the caller left out, exactly like the reactivating half
// of a full replace.
func (s *Store) UpsertWorkstations(ctx context.Context, tx pgx.Tx, workstations []auth.Workstation) ([]auth.Workstation, error) {
	result := make([]auth.Workstation, 0, len(workstations))
	for _, w := range workstations {
		row := tx.QueryRow(ctx, `
			INSERT INTO workstations (id, number, label, ip_address, active)
			VALUES ($1, $2, $3, $4::inet, true)
			ON CONFLICT (number) DO UPDATE SET label = EXCLUDED.label, ip_address = EXCLUDED.ip_address, active = true
			RETURNING `+workstationColumns,
			uuid.New(), w.Number, w.Label, w.IPAddress,
		)
		upserted, err := scanWorkstation(row)
		if err != nil {
			return nil, err
		}
		result = append(result, upserted)
	}
	return result, nil
}

// DeactivateWorkstationsNotIn sets active=false on every workstation whose
// number is not in keepNumbers — the deactivating half of "заменить
// список" (see UpsertWorkstations). An empty/nil keepNumbers deactivates
// every workstation, which is the correct "replace with an empty list"
// edge case, not a no-op: keepNumbers is normalized to a non-nil empty
// slice first, because pgx encodes a nil Go slice as SQL NULL, and
// `number <> ALL(NULL)` is NULL (excludes every row) rather than true.
func (s *Store) DeactivateWorkstationsNotIn(ctx context.Context, tx pgx.Tx, keepNumbers []int) error {
	if keepNumbers == nil {
		keepNumbers = []int{}
	}
	if _, err := tx.Exec(ctx, `UPDATE workstations SET active = false WHERE number <> ALL($1) AND active`, keepNumbers); err != nil {
		return auth.ErrStorage
	}
	return nil
}

// ------------------------------------------------------------ sessions

// InsertSession creates a session for userID (and, optionally,
// workstationID), valid for ttl from PostgreSQL's own clock — expires_at
// is computed in SQL (clock_timestamp() + ttl), not in Go, so the
// authoritative clock for session expiry is always the database's, per
// RFC-001's "Server/PostgreSQL time is authoritative" principle. session
// only needs ID/UserID/WorkstationID set on input; the returned Session
// carries the server-assigned CreatedAt/LastSeenAt/ExpiresAt.
func (s *Store) InsertSession(ctx context.Context, tx pgx.Tx, session auth.Session, ttl time.Duration) (auth.Session, error) {
	err := tx.QueryRow(ctx, `
		INSERT INTO sessions (id, user_id, workstation_id, expires_at)
		VALUES ($1, $2, $3, clock_timestamp() + make_interval(secs => $4))
		RETURNING created_at, last_seen_at, expires_at
	`, session.ID, session.UserID, session.WorkstationID, ttl.Seconds(),
	).Scan(&session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt)
	if err != nil {
		return auth.Session{}, auth.ErrStorage
	}
	return session, nil
}

// SessionByID looks a session up by its id (sha256(token) — see
// domain.go's Session.ID doc), joined with its user and workstation. Only
// a session whose expires_at is still after PostgreSQL's own clock
// matches: an expired session is indistinguishable from a missing one
// (auth.ErrNotFound either way), so a client can never use a distinct "expired"
// response to learn that a session id it guessed once existed.
func (s *Store) SessionByID(ctx context.Context, tx pgx.Tx, id []byte) (auth.SessionLookup, error) {
	row := tx.QueryRow(ctx, `
		SELECT
			s.id, s.user_id, s.workstation_id, s.created_at, s.last_seen_at, s.expires_at,
			u.id, u.login, u.password_hash, u.full_name, u.role, u.service_code, u.level, u.active, u.created_at,
			w.id, w.number, w.label, w.ip_address::text, w.active
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		LEFT JOIN workstations w ON w.id = s.workstation_id
		WHERE s.id = $1 AND s.expires_at > clock_timestamp()
	`, id)

	var lookup auth.SessionLookup
	var workstationID, wID *uuid.UUID
	var wNumber *int
	var wLabel, wIPAddress *string
	var wActive *bool
	err := row.Scan(
		&lookup.Session.ID, &lookup.Session.UserID, &workstationID, &lookup.Session.CreatedAt, &lookup.Session.LastSeenAt, &lookup.Session.ExpiresAt,
		&lookup.User.ID, &lookup.User.Login, &lookup.User.PasswordHash, &lookup.User.FullName, &lookup.User.Role, &lookup.User.ServiceCode, &lookup.User.Level, &lookup.User.Active, &lookup.User.CreatedAt,
		&wID, &wNumber, &wLabel, &wIPAddress, &wActive,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.SessionLookup{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.SessionLookup{}, auth.ErrStorage
	}
	lookup.Session.WorkstationID = workstationID
	if wID != nil {
		lookup.Workstation = &auth.Workstation{ID: *wID, Number: *wNumber, Label: *wLabel, IPAddress: wIPAddress, Active: *wActive}
	}
	return lookup, nil
}

// TouchSession extends a session's expiry by ttl from PostgreSQL's own
// clock, but only when it has not been touched within staleAfter — the
// sliding-renewal rule (renew when last_seen_at is more than a few
// minutes old, not on every single request) is enforced in the WHERE
// clause itself, so a session touched moments ago is simply a 0-row
// update, not an error.
func (s *Store) TouchSession(ctx context.Context, tx pgx.Tx, id []byte, staleAfter, ttl time.Duration) error {
	_, err := tx.Exec(ctx, `
		UPDATE sessions
		SET last_seen_at = clock_timestamp(), expires_at = clock_timestamp() + make_interval(secs => $3)
		WHERE id = $1 AND last_seen_at < clock_timestamp() - make_interval(secs => $2)
	`, id, staleAfter.Seconds(), ttl.Seconds())
	if err != nil {
		return auth.ErrStorage
	}
	return nil
}

func (s *Store) DeleteSession(ctx context.Context, tx pgx.Tx, id []byte) error {
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id); err != nil {
		return auth.ErrStorage
	}
	return nil
}

// DeleteUserSessions removes every session for userID — called when a
// password changes or the account is deactivated (CLAUDE.md-driven
// service.go behavior, C6), so a stolen or now-wrong-permission session
// stops working immediately rather than at its natural expiry.
func (s *Store) DeleteUserSessions(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return auth.ErrStorage
	}
	return nil
}

// ------------------------------------------------------------ audit

// AuditRecord is a thin pass-through to platform/audit.Record — it exists
// on auth.Store (not called directly by service.go) purely so a test fake
// can record an audit.Entry without a working pgx.Tx; see auth.Store's
// doc comment.
func (s *Store) AuditRecord(ctx context.Context, tx pgx.Tx, entry audit.Entry) error {
	return audit.Record(ctx, tx, entry)
}

// pgUniqueViolation is PostgreSQL's SQLSTATE for a unique_violation.
const pgUniqueViolation = "23505"
