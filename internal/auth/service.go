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

// Store is the narrow persistence port Service needs (CLAUDE.md: "declare
// [interfaces] near the consuming application service") — satisfied
// structurally by *internal/auth/postgres.Store, and by a fake in tests.
// Every method but WithTx takes an explicit pgx.Tx; see
// internal/auth/postgres/store.go's package doc for why.
//
// AuditRecord is here — a thin pass-through to audit.Record on the real
// adapter — rather than Service calling audit.Record directly, precisely
// so a fake Store can record an audit.Entry without a working pgx.Tx: the
// platform audit package's Record hands its pgx.Tx straight to
// tx.Exec, which a lightweight test fake cannot honor, and nearly every
// Service use case (Login, Logout — even a rejected login) writes one.
type Store interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error

	UserByLogin(ctx context.Context, tx pgx.Tx, login string) (User, error)
	UserByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (User, error)

	WorkstationByNumber(ctx context.Context, tx pgx.Tx, number int) (Workstation, error)
	WorkstationByID(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Workstation, error)

	InsertSession(ctx context.Context, tx pgx.Tx, session Session, ttl time.Duration) (Session, error)
	SessionByID(ctx context.Context, tx pgx.Tx, id []byte) (SessionLookup, error)
	TouchSession(ctx context.Context, tx pgx.Tx, id []byte, staleAfter, ttl time.Duration) error
	DeleteSession(ctx context.Context, tx pgx.Tx, id []byte) error

	AuditRecord(ctx context.Context, tx pgx.Tx, entry audit.Entry) error
}

// Service implements the auth module's login/logout/session use cases
// (slice-planning.md §2). Admin user/workstation management (CreateUser,
// UpdateUser, ReplaceWorkstations) is added in the next commit of slice 1.
type Service struct {
	store   Store
	limiter *LoginLimiter
	ttl     time.Duration
}

// NewService constructs a Service. ttl is the session lifetime (RFC-001
// §9/ADR-008: 12h by default — internal/platform/config.API.SessionTTL);
// limiter defaults to DefaultLoginLimiter (5/min) when nil.
func NewService(store Store, ttl time.Duration, limiter *LoginLimiter) *Service {
	if limiter == nil {
		limiter = DefaultLoginLimiter()
	}
	return &Service{store: store, limiter: limiter, ttl: ttl}
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

	user, workstation, err := s.verifyCredentials(ctx, req)
	if err != nil {
		s.auditRejectedLogin(ctx, nil, "", requestID, loginRejectionReason(err))
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

// verifyCredentials resolves login+password (and, for a trainee, the
// workstation) without writing anything. An unknown login still runs
// VerifyPassword — against dummyHash — so its timing matches a known
// login with a wrong password (password.go's dummyHash doc explains why).
func (s *Service) verifyCredentials(ctx context.Context, req LoginRequest) (User, *Workstation, error) {
	var user User
	var found bool
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		u, err := s.store.UserByLogin(ctx, tx, req.Login)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		user, found = u, true
		return nil
	})
	if err != nil {
		return User{}, nil, err
	}

	if !found {
		_, _ = VerifyPassword(dummyHash, req.Password)
		return User{}, nil, ErrInvalidCredentials
	}
	ok, err := VerifyPassword(user.PasswordHash, req.Password)
	if err != nil || !ok {
		return User{}, nil, ErrInvalidCredentials
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

// auditRejectedLogin best-effort records a rejected attempt; see Login's
// doc comment for why its own failure is not surfaced.
func (s *Service) auditRejectedLogin(ctx context.Context, actorID *uuid.UUID, actorRole, requestID, reason string) {
	_ = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		return s.store.AuditRecord(ctx, tx, audit.Entry{
			ActorID: actorID, ActorRole: actorRole, Action: "auth.login",
			ResourceType: "user", Outcome: audit.OutcomeRejected, RequestID: requestID,
			Details: map[string]any{"reason": reason},
		})
	})
}

// Logout deletes the session token names (if any) and audits it. It never
// fails the caller over an already-invalid or already-gone token — POST
// /auth/logout is idempotent by contract (openapi.yaml: always 204).
func (s *Service) Logout(ctx context.Context, token, requestID string) {
	id, err := sessionIDFromToken(token)
	if err != nil {
		return
	}
	_ = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lookup, lookupErr := s.store.SessionByID(ctx, tx, id)
		if err := s.store.DeleteSession(ctx, tx, id); err != nil {
			return err
		}
		if lookupErr != nil {
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
