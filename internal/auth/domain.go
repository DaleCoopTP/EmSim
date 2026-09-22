// Package auth is the auth product module (CLAUDE.md §"Architecture
// boundaries": users, sessions, roles, workstations; owns users,
// sessions, workstations — RFC-001 §4.2). This file holds the pure
// domain: value types and validation rules with no HTTP or PostgreSQL
// dependency (CLAUDE.md: "domain rules are independent of HTTP, SQL,
// files, STT, and LLM"). password.go, authz.go and ratelimit.go are
// domain too; the pgx-backed store and the HTTP handlers are separate
// packages (internal/auth/postgres, internal/auth/http) added in later
// commits of slice 1.
package auth

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Role is users.role (migrations/…_auth_users_workstations_sessions.sql,
// openapi.yaml Role).
type Role string

const (
	RoleAdmin      Role = "admin"
	RoleInstructor Role = "instructor"
	RoleTrainee    Role = "trainee"
)

func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleInstructor, RoleTrainee:
		return true
	default:
		return false
	}
}

// Level is users.level / runs.level_at_start (openapi.yaml Level): the
// trainee's current difficulty level. It is training state, not an
// account attribute — every user starts at LevelEasy and the only thing
// that changes it is an instructor applying a level recommendation
// (RFC-001 §7.4, slice 10), never the admin user API.
type Level string

const (
	LevelEasy   Level = "easy"
	LevelMedium Level = "medium"
	LevelHard   Level = "hard"
)

func (l Level) Valid() bool {
	switch l {
	case LevelEasy, LevelMedium, LevelHard:
		return true
	default:
		return false
	}
}

// User is one users row. ServiceCode is nil for admin/instructor and set
// for trainee (schema.sql comment: "профиль обучаемого; NULL для
// admin/instructor" — enforced by validateServiceCode below, not by the
// Go type system, since the rule depends on Role).
type User struct {
	ID           uuid.UUID
	Login        string
	PasswordHash string
	FullName     string
	Role         Role
	ServiceCode  *string
	Level        Level
	Active       bool
	CreatedAt    time.Time
}

// Workstation is one workstations row. IPAddress is the optional
// inet-bound РМ address (schema.sql); slice 1 stores it but does not use
// it to resolve a workstation at login (slice-planning.md §2: "Привязка
// РМ к IP-адресу... в этом срезе не реализуется").
type Workstation struct {
	ID        uuid.UUID
	Number    int
	Label     string
	IPAddress *string
	Active    bool
}

// Session is one sessions row. ID is the value the store looks a session
// up by — sha256 of the opaque token the client's cookie carries, never
// the token itself (the plan decision behind internal/auth/http/cookie.go,
// added later in slice 1): a leaked database dump must not hand out live
// sessions.
type Session struct {
	ID            []byte
	UserID        uuid.UUID
	WorkstationID *uuid.UUID
	CreatedAt     time.Time
	LastSeenAt    time.Time
	ExpiresAt     time.Time
}

// Principal is the authenticated identity a verified session resolves to
// (internal/auth/http/middleware.go) — what authorization and ownership
// checks need on every request, not the full User row. SessionExpiresAt
// is carried along too (Service.Authenticate already has it from the
// SessionByID lookup that produced this Principal) purely so Service.Me
// can answer GET /me's session_expires_at without a second session
// lookup.
type Principal struct {
	UserID           uuid.UUID
	Role             Role
	WorkstationID    *uuid.UUID
	SessionExpiresAt time.Time
}

// Me is the read model behind openapi.yaml's Me schema — returned by both
// POST /auth/login and GET /me.
type Me struct {
	User             User
	Workstation      *Workstation
	SessionExpiresAt time.Time
}

// SessionLookup is what the store's SessionByID returns: the session row
// joined with its user and (if any) workstation, so Authenticate/Me get
// everything they need in one round trip. It lives in this package (not
// internal/auth/postgres) because Service — the consumer — declares its
// own Store port here and cannot import its adapter without inverting the
// dependency (CLAUDE.md: domain/application code stays independent of the
// SQL adapter).
type SessionLookup struct {
	Session     Session
	User        User
	Workstation *Workstation
}

// NewUser is the input to creating a user (openapi.yaml UserCreate) — kept
// distinct from User because it carries a plaintext Password the domain
// never stores; the caller turns it into a PasswordHash with
// HashPassword before persisting a User. There is no Level: a new user
// always starts at LevelEasy (see Level).
type NewUser struct {
	Login       string
	Password    string
	FullName    string
	Role        Role
	ServiceCode *string
}

// Patch is a partial update to a user (openapi.yaml UserPatch). A nil
// field means "leave unchanged". ServiceCode is the one field where the
// zero value of its pointed-to string ("") is meaningful on its own: a
// service code is never legitimately empty, so *ServiceCode == "" means
// "clear it to NULL" and a non-empty string means "set it" — this avoids
// a double pointer for the one nullable field a client can explicitly
// clear.
type Patch struct {
	Password    *string
	FullName    *string
	Role        *Role
	ServiceCode *string
	Active      *bool
}

// UserUpdate is the storage-layer partial update for one user — lower-
// level than Patch: PasswordHash is already hashed (Service.UpdateUser
// calls HashPassword on Patch.Password before building this; the store
// never sees a plaintext password), and login is absent because it is
// immutable after creation (openapi.yaml UserPatch has no login field).
// Each field pairs a value with its own "touched" flag instead of relying
// on a nil pointer to mean "leave unchanged", so ServiceCode can be set to
// NULL without a double pointer — the same reasoning as Patch.ServiceCode,
// just spelled explicitly here since the store has no room for the
// empty-string convention (a stored NULL and a stored "" are genuinely
// different values, unlike at the Patch/API boundary).
type UserUpdate struct {
	PasswordHash    *string
	PasswordHashSet bool
	FullName        *string
	Role            *Role
	ServiceCode     *string
	ServiceCodeSet  bool
	Level           *Level
	Active          *bool
}

var (
	// ErrInvalidCredentials covers both an unknown login and a login/
	// password mismatch — the caller must never let a client tell the two
	// apart (RFC-001 §5 groups both under 401 Unauthorized).
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrUserInactive        = errors.New("user is inactive")
	ErrWorkstationRequired = errors.New("workstation is required")
	ErrWorkstationUnknown  = errors.New("workstation is unknown")
	ErrWorkstationInactive = errors.New("workstation is inactive")
	ErrLoginTaken          = errors.New("login is already taken")
	ErrLastAdmin           = errors.New("cannot deactivate or demote the last active admin")
	// ErrRateLimited is LoginLimiter's "no more attempts this window" —
	// RFC-001 §9's "5 попыток/мин".
	ErrRateLimited = errors.New("too many login attempts")
	// ErrSessionInvalid covers a missing cookie, a malformed token, and a
	// session the store could not find (which includes an expired one —
	// SessionByID does not distinguish "expired" from "never existed", see
	// internal/auth/postgres.Store.SessionByID).
	ErrSessionInvalid = errors.New("session is invalid or expired")

	// ErrValidation is what errors.Is matches against any *ValidationError
	// — a caller that only needs to know "this was a validation problem"
	// (to map it to httpapi.CodeValidationFailed, say) doesn't need to
	// unwrap the field/reason first.
	ErrValidation = errors.New("validation failed")

	// ErrNotFound and ErrStorage are the two port-level errors every Store
	// method (internal/auth/postgres.Store) returns instead of a raw
	// database error: ErrNotFound for a lookup that matched no row,
	// ErrStorage for anything else that went wrong at the database. They
	// live here, not in the postgres package, because Service (the
	// consumer of the Store port) needs to check for them without
	// importing its own adapter.
	ErrNotFound = errors.New("not found")
	ErrStorage  = errors.New("storage failure")
)

// ValidationError names the one field that failed and why. Reason is a
// short machine code (e.g. "required", "invalid", "too_long",
// "not_allowed"), not user-facing text.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed: field=%s reason=%s", e.Field, e.Reason)
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

func invalid(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}

// loginPattern mirrors schema.sql's users_login_shape CHECK and
// openapi.yaml UserCreate.login.
var loginPattern = regexp.MustCompile(`^[a-z0-9._-]{3,64}$`)

const maxFullNameLength = 200 // openapi.yaml UserCreate.full_name maxLength

// ValidateLogin checks login against the shape both the DB CHECK and the
// OpenAPI contract require.
func ValidateLogin(login string) error {
	if !loginPattern.MatchString(login) {
		return invalid("login", "invalid")
	}
	return nil
}

// ValidateFullName checks full_name is non-empty and within the OpenAPI
// contract's length limit.
func ValidateFullName(fullName string) error {
	trimmed := strings.TrimSpace(fullName)
	if trimmed == "" {
		return invalid("full_name", "required")
	}
	if len([]rune(fullName)) > maxFullNameLength {
		return invalid("full_name", "too_long")
	}
	return nil
}

// A trainee may have no service profile for operator 112. DDS assignment
// still requires a matching service_code and checks that at assignment/start.
// Admins and instructors never carry a service profile.
func validateServiceCode(role Role, serviceCode *string) error {
	present := serviceCode != nil && strings.TrimSpace(*serviceCode) != ""
	switch role {
	case RoleTrainee:
		if serviceCode != nil && !present {
			return invalid("service_code", "blank")
		}
	case RoleAdmin, RoleInstructor:
		if present {
			return invalid("service_code", "not_allowed")
		}
	}
	return nil
}

// ValidateNewUser checks every field of n in isolation, then the
// role/service_code combination together.
func ValidateNewUser(n NewUser) error {
	if err := ValidateLogin(n.Login); err != nil {
		return err
	}
	if err := validatePasswordLength(n.Password); err != nil {
		return err
	}
	if err := ValidateFullName(n.FullName); err != nil {
		return err
	}
	if !n.Role.Valid() {
		return invalid("role", "invalid")
	}
	return validateServiceCode(n.Role, n.ServiceCode)
}

// ValidateUserPatch checks patch in isolation, then — when patch changes
// Role or ServiceCode — that the resulting combination (patch.Role if set,
// else current.Role; patch's cleared/set ServiceCode if set, else
// current.ServiceCode) stays valid. It needs current because a patch that
// only changes, say, full_name must not be rejected over a service_code
// the request never touched.
func ValidateUserPatch(current User, patch Patch) error {
	if patch.Password != nil {
		if err := validatePasswordLength(*patch.Password); err != nil {
			return err
		}
	}
	if patch.FullName != nil {
		if err := ValidateFullName(*patch.FullName); err != nil {
			return err
		}
	}
	effectiveRole := current.Role
	if patch.Role != nil {
		if !patch.Role.Valid() {
			return invalid("role", "invalid")
		}
		effectiveRole = *patch.Role
	}
	if patch.Role == nil && patch.ServiceCode == nil {
		return nil
	}
	effectiveServiceCode := current.ServiceCode
	if patch.ServiceCode != nil {
		if *patch.ServiceCode == "" {
			effectiveServiceCode = nil
		} else {
			effectiveServiceCode = patch.ServiceCode
		}
	}
	return validateServiceCode(effectiveRole, effectiveServiceCode)
}
