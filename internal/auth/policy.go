package auth

import "time"

type Policy struct {
	LockoutAttempts   int
	LockoutDuration   time.Duration
	PasswordMinLength int
	ForceChangeRoles  []Role
	LoginsPerMinute   int
}

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

func (p Policy) checkPassword(password string) error {
	if len(password) < p.PasswordMinLength {
		return invalid("password", "too_short")
	}
	return nil
}
