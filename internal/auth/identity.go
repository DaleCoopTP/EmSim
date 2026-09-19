package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// IdentityProvider is the authentication port used by Service. The local
// password implementation below is the slice-1 adapter; a future LDAP/AD
// adapter can replace it without changing login/session orchestration.
type IdentityProvider interface {
	Authenticate(ctx context.Context, login, password string) (User, error)
}

// identityStore is the persistence subset the local password provider needs.
type identityStore interface {
	WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error
	UserByLogin(ctx context.Context, tx pgx.Tx, login string) (User, error)
}

type PasswordIdentityProvider struct {
	store identityStore
}

func NewPasswordIdentityProvider(store identityStore) *PasswordIdentityProvider {
	return &PasswordIdentityProvider{store: store}
}

// Authenticate verifies local Argon2id credentials. Unknown logins still pay
// the password-verification cost so they are not distinguishable by timing.
func (p *PasswordIdentityProvider) Authenticate(ctx context.Context, login, password string) (User, error) {
	var user User
	found := false
	err := p.store.WithTx(ctx, func(tx pgx.Tx) error {
		u, err := p.store.UserByLogin(ctx, tx, login)
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
		return User{}, err
	}
	if !found {
		_, _ = VerifyPassword(dummyHash, password)
		return User{}, ErrInvalidCredentials
	}
	ok, err := VerifyPassword(user.PasswordHash, password)
	if err != nil || !ok {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}
