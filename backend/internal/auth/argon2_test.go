package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPasswordProducesArgon2idPHCString(t *testing.T) {
	h, err := HashPassword("correct horse battery staple 1!")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("hash does not carry the current parameters: %q", h)
	}
	if !CheckPasswordHash("correct horse battery staple 1!", h) {
		t.Fatal("freshly created hash does not verify")
	}
	if CheckPasswordHash("wrong horse battery staple 1!", h) {
		t.Fatal("a wrong password verified")
	}
}

func TestHashPasswordSaltsEveryHash(t *testing.T) {
	const pw = "correct horse battery staple 1!"
	a, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("the same password produced identical hashes — salt is not random")
	}
	if !CheckPasswordHash(pw, a) || !CheckPasswordHash(pw, b) {
		t.Fatal("independently salted hashes must both verify")
	}
}

func TestLegacyBcryptHashesStillVerify(t *testing.T) {
	// Accounts created before the migration must keep working; anything else
	// would lock every existing user out on deploy.
	const pw = "legacy password 42!"
	legacy, err := bcrypt.GenerateFromPassword([]byte(pw), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPasswordHash(pw, string(legacy)) {
		t.Fatal("legacy bcrypt hash rejected")
	}
	if CheckPasswordHash("not the password", string(legacy)) {
		t.Fatal("legacy bcrypt hash accepted a wrong password")
	}
}

func TestNeedsRehashTargetsLegacyAndWeakerHashes(t *testing.T) {
	legacy, err := bcrypt.GenerateFromPassword([]byte("legacy password 42!"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !NeedsRehash(string(legacy)) {
		t.Fatal("a bcrypt hash should be marked for upgrade")
	}

	current, err := HashPassword("correct horse battery staple 1!")
	if err != nil {
		t.Fatal(err)
	}
	if NeedsRehash(current) {
		t.Fatal("a hash at the current parameters should not be re-hashed on every login")
	}

	// Same algorithm, weaker cost — still worth upgrading.
	weaker := "$argon2id$v=19$m=16384,t=1,p=1$c29tZXNhbHR2YWx1ZQ$ZGlnZXN0ZGlnZXN0ZGlnZXN0ZGlnZXN0MDA"
	if !NeedsRehash(weaker) {
		t.Fatal("a hash below the current parameters should be marked for upgrade")
	}
	if !NeedsRehash("not a hash at all") {
		t.Fatal("an unrecognised hash should be marked for upgrade")
	}
}

func TestCheckPasswordHashRejectsMalformedArgonStrings(t *testing.T) {
	// A stored hash is attacker-controlled the moment the database is, so
	// parsing must fail closed rather than authenticate or spend wild resources.
	for _, h := range []string{
		"$argon2id$",
		"$argon2id$v=19$m=65536,t=3,p=4$onlyfourfields",
		"$argon2id$v=99$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0$ZGlnZXN0",
		"$argon2id$v=19$m=0,t=0,p=0$c2FsdHNhbHRzYWx0$ZGlnZXN0",
		"$argon2id$v=19$m=99999999,t=3,p=4$c2FsdHNhbHRzYWx0$ZGlnZXN0",
		"$argon2id$v=19$m=65536,t=3,p=4$!!!notbase64!!!$ZGlnZXN0",
	} {
		if CheckPasswordHash("anything", h) {
			t.Fatalf("malformed hash authenticated: %q", h)
		}
	}
}

func TestPassphrasesLongerThanBcryptsLimitAreNotTruncated(t *testing.T) {
	// bcrypt stopped at 72 bytes, so two passphrases sharing a long prefix were
	// interchangeable. Argon2id must tell them apart.
	base := strings.Repeat("a", 72)
	h, err := HashPassword(base + "TAIL-ONE")
	if err != nil {
		t.Fatal(err)
	}
	if CheckPasswordHash(base+"TAIL-TWO", h) {
		t.Fatal("passwords differing only past byte 72 were treated as equal")
	}
	if !CheckPasswordHash(base+"TAIL-ONE", h) {
		t.Fatal("the correct long passphrase failed to verify")
	}
}

func TestLongPasswordCannotAuthenticateAgainstATruncatedLegacyHash(t *testing.T) {
	// bcrypt hashed only the first 72 bytes. Accepting a longer password
	// against such a hash would let anything sharing that prefix log in.
	prefix := strings.Repeat("a", 72)
	legacy, err := bcrypt.GenerateFromPassword([]byte(prefix), 10)
	if err != nil {
		t.Fatal(err)
	}
	if CheckPasswordHash(prefix+"anything-at-all", string(legacy)) {
		t.Fatal("a longer password authenticated against a truncated bcrypt hash")
	}
}

func TestHashPasswordRefusesUnboundedInput(t *testing.T) {
	if _, err := HashPassword(strings.Repeat("x", MaxPasswordLen+1)); err == nil {
		t.Fatal("an over-long password should be refused, not hashed")
	}
}
