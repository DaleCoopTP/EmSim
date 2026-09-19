package auth

import (
	"strings"
	"testing"
)

// testParams keeps unit tests fast: real argon2id cost (DefaultParams)
// would make dozens of hash/verify round trips noticeably slow. The
// format and comparison logic under test do not depend on the cost
// parameters themselves.
var testParams = Params{MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func TestHashPasswordVerifyPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple", testParams)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !ok {
		t.Fatal("VerifyPassword() = false for the correct password")
	}
}

func TestVerifyPasswordRejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple", testParams)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	ok, err := VerifyPassword(hash, "wrong password")
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword() = true for the wrong password")
	}
}

func TestHashPasswordProducesDistinctSaltsForSamePassword(t *testing.T) {
	first, err := HashPassword("same password", testParams)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	second, err := HashPassword("same password", testParams)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if first == second {
		t.Fatal("two hashes of the same password with fresh salts are identical")
	}
}

func TestHashPasswordEncodesPHCFormatWithParams(t *testing.T) {
	hash, err := HashPassword("a password", testParams)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("hash = %q, want a $argon2id$... prefix with the given params", hash)
	}
}

func TestVerifyPasswordUsesParamsEmbeddedInHashNotDefaultParams(t *testing.T) {
	// Hash with non-default params, then verify while DefaultParams is
	// something else entirely — VerifyPassword must still succeed because
	// it reads cost parameters from the hash, not from DefaultParams.
	custom := Params{MemoryKiB: 8 * 1024, Iterations: 3, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	hash, err := HashPassword("a password", custom)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	ok, err := VerifyPassword(hash, "a password")
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !ok {
		t.Fatal("VerifyPassword() = false for a hash made with non-default params")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	tests := []string{
		"",
		"not-a-hash-at-all",
		"$argon2id$v=19$m=8192,t=1,p=1$onlyonefield",
		"$bcrypt$v=19$m=8192,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=1$m=8192,t=1,p=1$c2FsdA$aGFzaA", // wrong version
		"$argon2id$v=19$garbage$c2FsdA$aGFzaA",
	}
	for _, hash := range tests {
		ok, err := VerifyPassword(hash, "anything")
		if err == nil {
			t.Errorf("VerifyPassword(%q) error = nil, want ErrPasswordHashInvalid", hash)
		}
		if ok {
			t.Errorf("VerifyPassword(%q) = true, want false for a malformed hash", hash)
		}
	}
}

func TestDummyHashIsAValidHashDistinctFromRealOnes(t *testing.T) {
	ok, err := VerifyPassword(dummyHash, "dummy-password-for-timing-alignment")
	if err != nil {
		t.Fatalf("VerifyPassword(dummyHash) error = %v", err)
	}
	if !ok {
		t.Fatal("dummyHash does not verify against its own placeholder password")
	}
	ok, err = VerifyPassword(dummyHash, "some random guess")
	if err != nil {
		t.Fatalf("VerifyPassword(dummyHash) error = %v", err)
	}
	if ok {
		t.Fatal("dummyHash verified against an unrelated password")
	}
}
