package auth

import "time"

// Policy is the login policy (ADR-038), set from .env by the composition:
// lock after LockoutAttempts wrong passwords in a row for LockoutDuration
// (0 attempts: never lock), a password no shorter than PasswordMinLength,
// and the roles whose administrator-set password must be changed at the
// next login.
type Policy struct {
	LockoutAttempts   int
	LockoutDuration   time.Duration
	PasswordMinLength int
	ForceChangeRoles  []Role
	// LoginsPerMinute replaces the per-login rate limit (default 5, RFC-001
	// §9); 0 keeps it. Test stacks raise it: their specs sign in as the same
	// admin many times a minute.
	LoginsPerMinute int
}

// DefaultPolicy is what a Service without WithPolicy uses.
func DefaultPolicy() Policy {
	return Policy{
		LockoutAttempts: 10, LockoutDuration: 15 * time.Minute,
		PasswordMinLength: minPasswordBytes, ForceChangeRoles: []Role{RoleAdmin, RoleInstructor},
	}
}

func (p Policy) forcesChange(role Role) bool {
	for _, r := range p.ForceChangeRoles {
		if r == role {
			return true
		}
	}
	return false
}

// checkPassword applies the configured minimum on top of ValidateNewUser's
// own bounds, which stay the floor.
func (p Policy) checkPassword(password string) error {
	if len(password) < p.PasswordMinLength {
		return invalid("password", "too_short")
	}
	return nil
}
