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

// Level is users.level / runs.level_at_start (openapi.yaml Level).
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
// (internal/auth/http/middleware.go, added later in slice 1) — only what
// authorization and ownership checks need, not the full User row.
type Principal struct {
	UserID        uuid.UUID
	Role          Role
	WorkstationID *uuid.UUID
}

// NewUser is the input to creating a user (openapi.yaml UserCreate) — kept
// distinct from User because it carries a plaintext Password the domain
// never stores; the caller turns it into a PasswordHash with
// HashPassword before persisting a User.
type NewUser struct {
	Login       string
	Password    string
	FullName    string
	Role        Role
	ServiceCode *string
	Level       Level // "" means unspecified; LevelOrDefault resolves it.
}

// LevelOrDefault returns n.Level, or LevelEasy when it was left
// unspecified — the same default users.level carries in the schema.
func (n NewUser) LevelOrDefault() Level {
	if n.Level == "" {
		return LevelEasy
	}
	return n.Level
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
	Level       *Level
	Active      *bool
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

	// ErrValidation is what errors.Is matches against any *ValidationError
	// — a caller that only needs to know "this was a validation problem"
	// (to map it to httpapi.CodeValidationFailed, say) doesn't need to
	// unwrap the field/reason first.
	ErrValidation = errors.New("validation failed")
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

// validateServiceCode enforces schema.sql's comment on users.service_code:
// required for trainee, must be absent for admin/instructor.
func validateServiceCode(role Role, serviceCode *string) error {
	present := serviceCode != nil && strings.TrimSpace(*serviceCode) != ""
	switch role {
	case RoleTrainee:
		if !present {
			return invalid("service_code", "required")
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
	if n.Level != "" && !n.Level.Valid() {
		return invalid("level", "invalid")
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
	if patch.Level != nil && !patch.Level.Valid() {
		return invalid("level", "invalid")
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
