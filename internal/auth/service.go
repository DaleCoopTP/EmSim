package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"emsim/internal/platform/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// sessionTokenBytes is the size of the random cookie value (32 bytes,
// matching the plan decision behind sessions.id: base64url(32 random
// bytes) as the cookie, sha256(token) — also 32 bytes — as what the
// database stores).
const sessionTokenBytes = 32

// staleAfter is the sliding-renewal threshold: a session touched more
// recently than this is left alone (RFC-001 plan decision: "если
// last_seen_at старше 5 мин — обновить").
const staleAfter = 5 * time.Minute

type Store interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error

	UserByLogin(ctx context.Context, tx pgx.Tx, login string) (User, error)
	UserByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (User, error)
	ListUsers(ctx context.Context, tx pgx.Tx, page, pageSize int) ([]User, int, error)
	InsertUser(ctx context.Context, tx pgx.Tx, u User) (User, error)
	UpdateUser(ctx context.Context, tx pgx.Tx, id uuid.UUID, update UserUpdate) (User, error)
	CountActiveAdmins(ctx context.Context, tx pgx.Tx) (int, error)

	WorkstationByNumber(ctx context.Context, tx pgx.Tx, number int) (Workstation, error)
	WorkstationByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Workstation, error)
	ListWorkstations(ctx context.Context, tx pgx.Tx) ([]Workstation, error)
	UpsertWorkstations(ctx context.Context, tx pgx.Tx, workstations []Workstation) ([]Workstation, error)
	DeactivateWorkstationsNotIn(ctx context.Context, tx pgx.Tx, keepNumbers []int) error

	InsertSession(ctx context.Context, tx pgx.Tx, session Session, ttl time.Duration) (Session, error)
	SessionByID(ctx context.Context, tx pgx.Tx, id []byte) (SessionLookup, error)
	TouchSession(ctx context.Context, tx pgx.Tx, id []byte, staleAfter, ttl time.Duration) error
	DeleteSession(ctx context.Context, tx pgx.Tx, id []byte) error
	DeleteUserSessions(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error
	DeleteOtherUserSessions(ctx context.Context, tx pgx.Tx, userID uuid.UUID, keep []byte) error
	ListUserSessions(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]SessionInfo, error)

	RecordFailedLogin(ctx context.Context, tx pgx.Tx, userID uuid.UUID, threshold int, lockFor time.Duration) (bool, error)
	ClearFailedLogins(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error

	AuditRecord(ctx context.Context, tx pgx.Tx, entry audit.Entry) error
}

// ServiceCatalog is the port CreateUser/UpdateUser use to check a
// trainee's service_code against content's services table before
// writing it (openapi.yaml's 422 "service_code: unknown",
// slice-2-plan.md's C5). Declared here (auth, the consumer) rather than
// in internal/content — the same reasoning as Store: content.Service
// satisfies this structurally without importing auth.
type ServiceCatalog interface {
	ServiceExists(ctx context.Context, code string) (bool, error)
}

// Service implements the auth module's login/session use cases
// (slice-planning.md §2) and its admin user/workstation management
// (CreateUser, UpdateUser, ListUsers, ListWorkstations,
// ReplaceWorkstations — see admin.go).
type Service struct {
	store            Store
	identityProvider IdentityProvider
	limiter          *LoginLimiter
	ttl              time.Duration
	catalog          ServiceCatalog
	policy           Policy
	now              func() time.Time
}

// NewService constructs a Service. ttl is the session lifetime (RFC-001
// §9/ADR-008: 12h by default — internal/platform/config.API.SessionTTL);
// limiter defaults to DefaultLoginLimiter (5/min) when nil. catalog may
// be nil — cmd/emsim/bootstrap.go's Service never sets a service_code
// (BootstrapAdmin always creates an admin), so it has no content module
// to wire in; CreateUser/UpdateUser's service_code check is a no-op
// without one. The real api process (cmd/emsim/api.go) always wires a
// real catalog.
func NewService(store Store, identityProvider IdentityProvider, ttl time.Duration, limiter *LoginLimiter, catalog ServiceCatalog) *Service {
	if identityProvider == nil {
		identityProvider = NewPasswordIdentityProvider(store)
	}
	if limiter == nil {
		limiter = DefaultLoginLimiter()
	}
	return &Service{store: store, identityProvider: identityProvider, limiter: limiter, ttl: ttl, catalog: catalog, policy: DefaultPolicy(), now: time.Now}
}

func (s *Service) WithPolicy(p Policy) *Service {
	s.policy = p
	if p.LoginsPerMinute > 0 {
		s.limiter = NewLoginLimiter(p.LoginsPerMinute, time.Minute, nil)
	}
	return s
}

// LoginRequest is POST /auth/login's body (openapi.yaml).
type LoginRequest struct {
	Login         string
	Password      string
	WorkstationNo *int
}

// LoginResult is what a successful Login hands the HTTP layer: Token is
// the raw, unhashed cookie value — the only place it ever exists outside
// the client's browser, since InsertSession only ever receives its hash.
type LoginResult struct {
	Token     string
	ExpiresAt time.Time
	Me        Me
}

// Login authenticates login/password (and, for a trainee, resolves
// WorkstationNo to a workstation), creates a session, and audits the
// attempt. requestID is threaded through to the audit_log row
// (httpapi.RequestIDFromContext at the HTTP layer).
//
// Every login attempt is rate-limited by login before anything else runs
// (RFC-001 §9), and a rejected attempt — rate-limited or not — is audited
// in its own short transaction, since there is no session to bundle it
// with; a successful attempt commits its session insert and its ok-outcome
// audit row together in one transaction. A failure auditing a rejected
// attempt is not itself surfaced to the caller: the credential/rate-limit
// error it already has is the one that matters to the client, and the
// audit write is best-effort here (no logger is threaded into Service to
// report it — see cmd/emsim's composition for where that could be added
// if this needs tightening later).
func (s *Service) Login(ctx context.Context, req LoginRequest, requestID string) (LoginResult, error) {
	if !s.limiter.Allow(req.Login) {
		s.auditRejectedLogin(ctx, nil, "", requestID, "rate_limited")
		return LoginResult{}, ErrRateLimited
	}

	known, knownFound := s.lookupUser(ctx, req.Login)
	if knownFound && known.LockedUntil != nil && known.LockedUntil.After(s.now()) {
		s.auditRejectedLogin(ctx, &known.ID, "", requestID, "account_locked")
		return LoginResult{}, ErrAccountLocked
	}

	user, workstation, err := s.verifyCredentials(ctx, req)
	if err != nil {
		var subject *uuid.UUID
		if knownFound {
			subject = &known.ID
		}
		s.auditRejectedLogin(ctx, subject, "", requestID, loginRejectionReason(err))
		if knownFound && errors.Is(err, ErrInvalidCredentials) {
			s.registerFailedLogin(ctx, known, requestID)
		}
		return LoginResult{}, err
	}

	token := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(token); err != nil {
		return LoginResult{}, ErrStorage
	}
	id := sha256.Sum256(token)

	var workstationID *uuid.UUID
	if workstation != nil {
		wsID := workstation.ID
		workstationID = &wsID
	}

	var result LoginResult
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		session, err := s.store.InsertSession(ctx, tx, Session{ID: id[:], UserID: user.ID, WorkstationID: workstationID}, s.ttl)
		if err != nil {
			return err
		}
		if user.FailedLogins > 0 {
			if err := s.store.ClearFailedLogins(ctx, tx, user.ID); err != nil {
				return err
			}
		}
		if err := s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &user.ID, ActorRole: string(user.Role), Action: "auth.login",
			ResourceType: "user", ResourceID: &user.ID, Outcome: audit.OutcomeOK, RequestID: requestID,
		}); err != nil {
			return err
		}
		result = LoginResult{
			Token:     base64.RawURLEncoding.EncodeToString(token),
			ExpiresAt: session.ExpiresAt,
			Me:        Me{User: user, Workstation: workstation, SessionExpiresAt: session.ExpiresAt},
		}
		return nil
	})
	if err != nil {
		return LoginResult{}, err
	}
	return result, nil
}

func (s *Service) lookupUser(ctx context.Context, login string) (User, bool) {
	var user User
	found := false
	_ = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		u, err := s.store.UserByLogin(ctx, tx, login)
		if err == nil {
			user, found = u, true
		}
		return nil
	})
	return user, found
}

func (s *Service) registerFailedLogin(ctx context.Context, user User, requestID string) {
	if s.policy.LockoutAttempts <= 0 {
		return
	}
	_ = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		locked, err := s.store.RecordFailedLogin(ctx, tx, user.ID, s.policy.LockoutAttempts, s.policy.LockoutDuration)
		if err != nil || !locked {
			return err
		}
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorRole: string(user.Role), Action: "auth.lockout", ResourceType: "user", ResourceID: &user.ID,
			Outcome: audit.OutcomeRejected, RequestID: requestID,
			Details: map[string]any{"attempts": s.policy.LockoutAttempts, "minutes": int(s.policy.LockoutDuration / time.Minute)},
		})
	})
}

// verifyCredentials delegates login+password verification to the configured
// IdentityProvider, then applies EmSim's local account and workstation rules.
func (s *Service) verifyCredentials(ctx context.Context, req LoginRequest) (User, *Workstation, error) {
	user, err := s.identityProvider.Authenticate(ctx, req.Login, req.Password)
	if err != nil {
		return User{}, nil, err
	}
	if !user.Active {
		return User{}, nil, ErrUserInactive
	}

	workstation, err := s.resolveWorkstation(ctx, user.Role, req.WorkstationNo)
	if err != nil {
		return User{}, nil, err
	}
	return user, workstation, nil
}

// resolveWorkstation implements RFC-001 §5's login rule: workstation_no is
// required and validated for a trainee, ignored for admin/instructor.
func (s *Service) resolveWorkstation(ctx context.Context, role Role, workstationNo *int) (*Workstation, error) {
	if role != RoleTrainee {
		return nil, nil
	}
	if workstationNo == nil {
		return nil, ErrWorkstationRequired
	}

	var workstation Workstation
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		w, err := s.store.WorkstationByNumber(ctx, tx, *workstationNo)
		if errors.Is(err, ErrNotFound) {
			return ErrWorkstationUnknown
		}
		if err != nil {
			return err
		}
		workstation = w
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !workstation.Active {
		return nil, ErrWorkstationInactive
	}
	return &workstation, nil
}

func loginRejectionReason(err error) string {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		return "invalid_credentials"
	case errors.Is(err, ErrUserInactive):
		return "user_inactive"
	case errors.Is(err, ErrWorkstationRequired):
		return "workstation_required"
	case errors.Is(err, ErrWorkstationUnknown):
		return "workstation_unknown"
	case errors.Is(err, ErrWorkstationInactive):
		return "workstation_inactive"
	default:
		return "error"
	}
}

func (s *Service) auditRejectedLogin(ctx context.Context, subject *uuid.UUID, actorRole, requestID, reason string) {
	_ = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorRole: actorRole, Action: "auth.login",
			ResourceType: "user", ResourceID: subject, Outcome: audit.OutcomeRejected, RequestID: requestID,
			Details: map[string]any{"reason": reason},
		})
	})
}

// Logout deletes the named session (if any) and audits it. Malformed and
// already-gone tokens remain idempotent success, while storage/audit failures
// are returned so the HTTP layer cannot claim the session was revoked.
func (s *Service) Logout(ctx context.Context, token, requestID string) error {
	id, err := sessionIDFromToken(token)
	if err != nil {
		return nil
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lookup, lookupErr := s.store.SessionByID(ctx, tx, id)
		if lookupErr != nil && !errors.Is(lookupErr, ErrNotFound) {
			return lookupErr
		}
		if err := s.store.DeleteSession(ctx, tx, id); err != nil {
			return err
		}
		if errors.Is(lookupErr, ErrNotFound) {
			return nil
		}
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &lookup.User.ID, ActorRole: string(lookup.User.Role), Action: "auth.logout",
			ResourceType: "user", ResourceID: &lookup.User.ID, Outcome: audit.OutcomeOK, RequestID: requestID,
		})
	})
}

// Authenticate verifies token (the raw cookie value) against the session
// store and applies the sliding-renewal rule as a side effect. A missing,
// malformed, unknown, or expired token all collapse into ErrSessionInvalid
// — RFC-001 §5 treats them as one 401 Unauthorized, not four distinguishable
// outcomes a client could use to probe session ids.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	id, err := sessionIDFromToken(token)
	if err != nil {
		return Principal{}, err
	}

	var lookup SessionLookup
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.store.SessionByID(ctx, tx, id); err != nil {
			return err
		}
		if err := s.store.TouchSession(ctx, tx, id, staleAfter, s.ttl); err != nil {
			return err
		}
		// Re-read after the possible renewal so ExpiresAt reflects it —
		// see Principal.SessionExpiresAt's doc comment.
		l, err := s.store.SessionByID(ctx, tx, id)
		if err != nil {
			return err
		}
		lookup = l
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return Principal{}, ErrSessionInvalid
	}
	if err != nil {
		return Principal{}, err
	}
	if !lookup.User.Active {
		return Principal{}, ErrSessionInvalid
	}

	var workstationID *uuid.UUID
	if lookup.Workstation != nil {
		wsID := lookup.Workstation.ID
		workstationID = &wsID
	}
	return Principal{
		UserID: lookup.User.ID, Role: lookup.User.Role, WorkstationID: workstationID,
		SessionExpiresAt: lookup.Session.ExpiresAt,
		SessionID:        lookup.Session.ID, MustChangePassword: lookup.User.MustChangePassword,
	}, nil
}

// Me renders the full read model behind GET /me from an already-verified
// Principal (Authenticate's result) — a fresh read, not whatever
// Authenticate saw, since role/name/workstation label can have changed
// since the session was created.
func (s *Service) Me(ctx context.Context, principal Principal) (Me, error) {
	var me Me
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		user, err := s.store.UserByID(ctx, tx, principal.UserID)
		if err != nil {
			return err
		}
		var workstation *Workstation
		if principal.WorkstationID != nil {
			w, err := s.store.WorkstationByID(ctx, tx, *principal.WorkstationID)
			if err != nil {
				return err
			}
			workstation = &w
		}
		me = Me{User: user, Workstation: workstation, SessionExpiresAt: principal.SessionExpiresAt}
		return nil
	})
	if err != nil {
		return Me{}, err
	}
	return me, nil
}

// sessionIDFromToken decodes the cookie's raw value and returns
// sha256(token) — the value sessions.id actually stores (domain.go's
// Session.ID doc). A token of the wrong shape can never match a real
// session, so it fails closed with ErrSessionInvalid before any query.
func sessionIDFromToken(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != sessionTokenBytes {
		return nil, ErrSessionInvalid
	}
	id := sha256.Sum256(raw)
	return id[:], nil
}

func (s *Service) ChangePassword(ctx context.Context, principal Principal, current, next, requestID string) error {
	if err := s.policy.checkPassword(next); err != nil {
		return err
	}
	if err := validatePasswordLength(next); err != nil {
		return err
	}
	if next == current {
		return invalid("password", "same_as_current")
	}
	hash, err := HashPassword(next, DefaultParams)
	if err != nil {
		return ErrStorage
	}
	wrongCurrent := false
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		user, err := s.store.UserByID(ctx, tx, principal.UserID)
		if err != nil {
			return err
		}
		if ok, verr := VerifyPassword(user.PasswordHash, current); verr != nil || !ok {
			wrongCurrent = true
			return s.store.AuditRecord(ctx, tx, audit.Entry{
				ActorID: &user.ID, ActorRole: string(user.Role), Action: "auth.password_change",
				ResourceType: "user", ResourceID: &user.ID, Outcome: audit.OutcomeRejected, RequestID: requestID,
				Details: map[string]any{"reason": "invalid_credentials"},
			})
		}
		off := false
		if _, err := s.store.UpdateUser(ctx, tx, user.ID, UserUpdate{PasswordHash: &hash, PasswordHashSet: true, MustChangePassword: &off}); err != nil {
			return err
		}
		if err := s.store.DeleteOtherUserSessions(ctx, tx, user.ID, principal.SessionID); err != nil {
			return err
		}
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: &user.ID, ActorRole: string(user.Role), Action: "auth.password_change",
			ResourceType: "user", ResourceID: &user.ID, Outcome: audit.OutcomeOK, RequestID: requestID,
		})
	})
	if err == nil && wrongCurrent {
		return invalid("current_password", "incorrect")
	}
	return err
}
