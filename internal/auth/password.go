package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params are the argon2id cost parameters (ADR-008: "argon2id"; OWASP's
// current baseline for argon2id is m=19 MiB, t=2, p=1). They are embedded
// in every hash string HashPassword produces, so changing DefaultParams
// only affects newly hashed passwords — VerifyPassword always re-derives
// with the parameters recorded inside the hash it is checking, never with
// DefaultParams, so an old hash keeps verifying correctly after a
// parameter change.
type Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams is the OWASP argon2id baseline used for every password
// HashPassword is asked to hash without an explicit override.
var DefaultParams = Params{
	MemoryKiB:   19 * 1024,
	Iterations:  2,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

var (
	ErrPasswordHashInvalid = errors.New("password hash is malformed")
)

// maxPasswordBytes mirrors openapi.yaml UserCreate.password maxLength; the
// minimum (8) is enforced by validatePasswordLength below, not here — this
// only bounds HashPassword's own input.
const (
	minPasswordBytes = 8
	maxPasswordBytes = 256
)

func validatePasswordLength(password string) error {
	if len(password) < minPasswordBytes {
		return invalid("password", "too_short")
	}
	if len(password) > maxPasswordBytes {
		return invalid("password", "too_long")
	}
	return nil
}

// HashPassword returns a PHC-formatted argon2id hash
// ("$argon2id$v=19$m=...,t=...,p=...$salt$hash") of password using params.
func HashPassword(password string, params Params) (string, error) {
	salt := make([]byte, params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, params.Iterations, params.MemoryKiB, params.Parallelism, params.KeyLength)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.MemoryKiB, params.Iterations, params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword reports whether password matches encodedHash. A
// malformed hash fails closed — it returns (false, ErrPasswordHashInvalid)
// rather than panicking — and the comparison is constant-time so a
// mismatch's timing does not leak how much of the hash matched.
func VerifyPassword(encodedHash, password string) (bool, error) {
	params, salt, hash, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}
	candidate := argon2.IDKey([]byte(password), salt, params.Iterations, params.MemoryKiB, params.Parallelism, uint32(len(hash)))
	return subtle.ConstantTimeCompare(candidate, hash) == 1, nil
}

func decodeHash(encoded string) (Params, []byte, []byte, error) {
	// "$argon2id$v=19$m=...,t=...,p=...$salt$hash" splits on '$' into
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", "salt", "hash"].
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Params{}, nil, nil, ErrPasswordHashInvalid
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Params{}, nil, nil, ErrPasswordHashInvalid
	}
	var params Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.MemoryKiB, &params.Iterations, &params.Parallelism); err != nil {
		return Params{}, nil, nil, ErrPasswordHashInvalid
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, ErrPasswordHashInvalid
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, ErrPasswordHashInvalid
	}
	params.SaltLength = uint32(len(salt))
	params.KeyLength = uint32(len(hash))
	return params, salt, hash, nil
}

// dummyHash is a fixed, validly-formatted argon2id hash of a placeholder
// password, hashed once at package init with DefaultParams. service.go's
// Login calls VerifyPassword against it when the login it was given does
// not exist, so a request for an unknown login costs the same as one for
// a known login with a wrong password — the two must be indistinguishable
// by timing, not just by the ErrInvalidCredentials they both return.
var dummyHash = mustHashDummy()

func mustHashDummy() string {
	hash, err := HashPassword("dummy-password-for-timing-alignment", DefaultParams)
	if err != nil {
		panic("auth: failed to precompute dummy password hash: " + err.Error())
	}
	return hash
}
