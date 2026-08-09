package auth

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testSecret is a valid 64-char (256-bit) hex secret. Set before any token op
// because getJWTSecret loads JWT_SECRET exactly once via sync.Once.
const testSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestMain(m *testing.M) {
	os.Setenv("JWT_SECRET", testSecret)
	MustInitSecrets() // loads the secret + pre-computes dummyHash
	os.Exit(m.Run())
}

func TestHashAndCheckPassword(t *testing.T) {
	const pw = "Correct-Horse-9!"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == pw {
		t.Fatal("hash must not equal the plaintext")
	}
	if !CheckPasswordHash(pw, hash) {
		t.Error("correct password rejected")
	}
	if CheckPasswordHash("wrong-password", hash) {
		t.Error("wrong password accepted")
	}
}

func TestHashPassword_RejectsOver72Bytes(t *testing.T) {
	// bcrypt silently truncates at 72 bytes; we reject to stay predictable.
	long := make([]byte, 73)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := HashPassword(string(long)); err == nil {
		t.Error("expected error for password > 72 bytes")
	}
}

func TestCheckPasswordHash_RejectsOver72Bytes(t *testing.T) {
	hash, _ := HashPassword("Valid-Pass-12!")
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'b'
	}
	if CheckPasswordHash(string(long), hash) {
		t.Error("over-length password must not match")
	}
}

func TestGenerateAndValidateToken_RoundTrip(t *testing.T) {
	tok, err := GenerateToken(42, 7)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	claims, err := ValidateToken(tok)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.UserID != 42 || claims.Issuer != tokenIssuer || len(claims.Audience) != 1 || claims.Audience[0] != tokenAudience {
		t.Errorf("claims not preserved: %+v", claims)
	}
	if claims.TokenVersion != 7 {
		t.Errorf("token_version not preserved: got %d, want 7", claims.TokenVersion)
	}
}

// TestValidateToken_RejectsAlgNone is the headline security test: a forged
// token with "alg":"none" must be rejected, not trusted.
func TestValidateToken_RejectsAlgNone(t *testing.T) {
	claims := &Claims{
		UserID: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := ValidateToken(signed); err == nil {
		t.Fatal("alg=none token was accepted — algorithm confusion vulnerability")
	}
}

func TestValidateToken_RejectsDifferentHMACAlgorithm(t *testing.T) {
	claims := &Claims{
		UserID: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS384, claims)
	signed, err := tok.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign HS384: %v", err)
	}
	if _, err := ValidateToken(signed); err == nil {
		t.Fatal("HS384 token was accepted even though only HS256 is permitted")
	}
}

func TestValidateToken_RejectsMissingOrInvalidRequiredClaims(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		claims *Claims
	}{
		{
			name: "missing issuer",
			claims: &Claims{UserID: 1, RegisteredClaims: jwt.RegisteredClaims{
				Audience: jwt.ClaimStrings{tokenAudience}, ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
				IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now),
			}},
		},
		{
			name: "missing audience",
			claims: &Claims{UserID: 1, RegisteredClaims: jwt.RegisteredClaims{
				Issuer: tokenIssuer, ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
				IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now),
			}},
		},
		{
			name: "missing issued at",
			claims: &Claims{UserID: 1, RegisteredClaims: jwt.RegisteredClaims{
				Issuer: tokenIssuer, Audience: jwt.ClaimStrings{tokenAudience},
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), NotBefore: jwt.NewNumericDate(now),
			}},
		},
		{
			name: "non-positive user id",
			claims: &Claims{RegisteredClaims: jwt.RegisteredClaims{
				Issuer: tokenIssuer, Audience: jwt.ClaimStrings{tokenAudience},
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(now),
				NotBefore: jwt.NewNumericDate(now),
			}},
		},
		{
			name: "overlong lifetime",
			claims: &Claims{UserID: 1, RegisteredClaims: jwt.RegisteredClaims{
				Issuer: tokenIssuer, Audience: jwt.ClaimStrings{tokenAudience},
				ExpiresAt: jwt.NewNumericDate(now.Add(TokenTTL + time.Minute)), IssuedAt: jwt.NewNumericDate(now),
				NotBefore: jwt.NewNumericDate(now),
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok := jwt.NewWithClaims(jwt.SigningMethodHS256, tt.claims)
			signed, err := tok.SignedString([]byte(testSecret))
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			if _, err := ValidateToken(signed); err == nil {
				t.Fatal("token with invalid required claims was accepted")
			}
		})
	}
}

func TestValidateToken_RejectsWrongSecret(t *testing.T) {
	claims := &Claims{
		UserID: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte("a-totally-different-secret-32chars!!"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := ValidateToken(signed); err == nil {
		t.Fatal("token signed with a different secret was accepted")
	}
}

func TestValidateToken_RejectsExpired(t *testing.T) {
	claims := &Claims{
		UserID: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := ValidateToken(signed); err == nil {
		t.Fatal("expired token was accepted")
	}
}

func TestValidateToken_RejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "not.a.jwt", "ey.malformed", "a.b.c"} {
		if _, err := ValidateToken(s); err == nil {
			t.Errorf("garbage token accepted: %q", s)
		}
	}
}

func TestGenerateVerificationToken(t *testing.T) {
	a := GenerateVerificationToken()
	b := GenerateVerificationToken()
	if len(a) != 64 { // 32 random bytes hex-encoded
		t.Errorf("expected 64 hex chars, got %d", len(a))
	}
	if a == b {
		t.Error("two verification tokens collided — randomness broken")
	}
}

func TestAuditReferenceIsStableScopedAndOpaque(t *testing.T) {
	email := "person@example.com"
	first := AuditReference("email", email)
	second := AuditReference("email", email)
	ipScoped := AuditReference("ip", email)

	if first != second {
		t.Fatal("same audit subject should produce a stable reference")
	}
	if first == ipScoped {
		t.Fatal("audit reference must be domain-separated by identifier kind")
	}
	if strings.Contains(first, email) {
		t.Fatal("audit reference exposed the raw identifier")
	}
	if !strings.HasPrefix(first, "hmac-sha256:v1:") {
		t.Fatalf("unexpected audit reference format: %q", first)
	}
}

func TestCheckAgainstDummy_DoesNotPanic(t *testing.T) {
	// Sole purpose is constant-time consumption; must never panic or block.
	CheckAgainstDummy("anything")
}
