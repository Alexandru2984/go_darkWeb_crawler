package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

const (
	// MinSecretLen is the minimum accepted length for JWT_SECRET (32 hex characters = 128 bits of entropy).
	MinSecretLen = 32
	// TokenTTL is the lifetime of a JWT issued at login.
	TokenTTL      = 4 * time.Hour
	tokenIssuer   = "onion-spider"
	tokenAudience = "onion-spider-api"
)

var (
	jwtSecretOnce sync.Once
	jwtSecret     []byte
)

// getJWTSecret loads JWT_SECRET LAZILY — after main.go has called godotenv.Load().
// If the secret is missing or too short, the application refuses to start.
// NO fallback — a weak secret is equivalent to having no authentication.
func getJWTSecret() []byte {
	jwtSecretOnce.Do(func() {
		s := os.Getenv("JWT_SECRET")
		if len(s) < MinSecretLen {
			slog.Error("jwt_secret_invalid", "min_chars", MinSecretLen, "hint", "generate with: openssl rand -hex 32")
			os.Exit(1)
		}
		jwtSecret = []byte(s)
	})
	return jwtSecret
}

// MustInitSecrets forces loading of JWT_SECRET at application startup,
// after environment variables are available. If the secret is missing
// or too short, log.Fatal is triggered here, not on the first login.
// Also pre-computes dummyHash to equalize timing on login with a non-existent email.
func MustInitSecrets() {
	_ = getJWTSecret()
	h, err := HashPassword("dummy-timing-equalization-placeholder")
	if err != nil {
		slog.Error("dummy_hash_generation_failed", "err", err)
		os.Exit(1)
	}
	dummyHash = h
}

// dummyHash is a valid Argon2id hash used as the verification target when a
// user does not exist — prevents a timing attack for email enumeration.
var dummyHash string

// CheckAgainstDummy verifies a password against a dummy hash. The answer is
// always false; the sole purpose is to consume the time a real check would, so
// that "no such account" and "wrong password" are indistinguishable.
func CheckAgainstDummy(password string) {
	if dummyHash == "" {
		return // should not happen after MustInitSecrets, but acts as a safe-guard
	}
	_ = CheckPasswordHash(password, dummyHash)
}

type Claims struct {
	UserID int `json:"user_id"`
	// TokenVersion is compared against the user's current token_version in the
	// DB on each authenticated request. A mismatch (because the version was
	// bumped on password reset / logout-all) invalidates the token immediately.
	TokenVersion int `json:"tv"`
	jwt.RegisteredClaims
}

// Validate adds application invariants to the standard JWT checks. Requiring
// both temporal claims and bounding the issued lifetime prevents malformed or
// accidentally overlong credentials from becoming accepted sessions.
func (c Claims) Validate() error {
	if c.UserID <= 0 {
		return errors.New("invalid user id")
	}
	if c.TokenVersion < 0 {
		return errors.New("invalid token version")
	}
	if c.IssuedAt == nil || c.NotBefore == nil {
		return errors.New("missing required temporal claims")
	}
	if c.ExpiresAt != nil && c.ExpiresAt.Time.Sub(c.IssuedAt.Time) > TokenTTL {
		return errors.New("token lifetime exceeds maximum")
	}
	return nil
}

// Argon2id parameters. This is the second configuration recommended by RFC 9106
// (t=3, 64 MiB, p=4), which on this host costs about 190 ms per hash against
// bcrypt cost 12's 300 ms. Staying in the same order of magnitude matters during
// the migration: an unknown account is answered with an Argon2id dummy check, so
// a much cheaper Argon2id would make "no such user" measurably faster than "user
// with a legacy hash" and hand back the account enumeration the dummy exists to
// prevent.
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// MaxPasswordLen bounds what will be hashed. Argon2id has no 72-byte truncation
// to work around, so passphrases are welcome, but the input still gets read and
// mixed and an unbounded one is free work for an attacker to hand us.
const MaxPasswordLen = 1024

// argonSem bounds how many Argon2id hashes run at once. Each holds 64 MiB, so
// an unbounded login burst would trade the CPU-exhaustion risk bcrypt had for a
// memory-exhaustion one. Waiting behind this semaphore is bounded by the login
// rate limits in front of it.
var argonSem = make(chan struct{}, 4)

// ErrPasswordTooLong is returned rather than silently hashing a truncated value.
var ErrPasswordTooLong = errors.New("password exceeds maximum length")

// HashPassword hashes a password with Argon2id and returns it in the standard
// PHC string format, which carries the parameters and salt alongside the digest
// so future parameter changes stay verifiable against existing hashes.
func HashPassword(password string) (string, error) {
	if len(password) > MaxPasswordLen {
		return "", ErrPasswordTooLong
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argonKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// argonKey runs the KDF while holding a slot in the concurrency bound.
func argonKey(password, salt []byte, t, m uint32, p uint8, keyLen uint32) []byte {
	argonSem <- struct{}{}
	defer func() { <-argonSem }()
	return argon2.IDKey(password, salt, t, m, p, keyLen)
}

// CheckPasswordHash verifies a password against either an Argon2id PHC string
// or a legacy bcrypt hash. Accounts created before the migration keep working
// and are upgraded on their next successful login — see NeedsRehash.
func CheckPasswordHash(password, hash string) bool {
	if len(password) > MaxPasswordLen {
		return false
	}
	if strings.HasPrefix(hash, "$argon2id$") {
		return checkArgon2id(password, hash)
	}
	// bcrypt silently truncates at 72 bytes, so a longer password must not be
	// allowed to authenticate on the strength of its first 72.
	if len(password) > 72 {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func checkArgon2id(password, hash string) bool {
	params, salt, want, err := parseArgon2id(hash)
	if err != nil {
		return false
	}
	got := argonKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func parseArgon2id(hash string) (argonParams, []byte, []byte, error) {
	var p argonParams
	parts := strings.Split(hash, "$")
	// "", "argon2id", "v=19", "m=...,t=...,p=...", salt, digest
	if len(parts) != 6 || parts[1] != "argon2id" {
		return p, nil, nil, errors.New("malformed argon2id hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return p, nil, nil, errors.New("malformed argon2id version")
	}
	if version != argon2.Version {
		return p, nil, nil, errors.New("unsupported argon2id version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return p, nil, nil, errors.New("malformed argon2id parameters")
	}
	if p.memory == 0 || p.time == 0 || p.threads == 0 {
		return p, nil, nil, errors.New("invalid argon2id parameters")
	}
	// Refuse to spend arbitrary memory because a stored string asked us to: a
	// hash is attacker-controlled data the moment the database is.
	if p.memory > 1024*1024 || p.time > 16 {
		return p, nil, nil, errors.New("argon2id parameters out of range")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return p, nil, nil, errors.New("malformed argon2id salt")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return p, nil, nil, errors.New("malformed argon2id digest")
	}
	return p, salt, key, nil
}

// NeedsRehash reports whether a stored hash should be replaced after the user
// next proves the password. That covers legacy bcrypt hashes and Argon2id
// hashes made with weaker parameters than the current ones.
func NeedsRehash(hash string) bool {
	if !strings.HasPrefix(hash, "$argon2id$") {
		return true
	}
	p, _, _, err := parseArgon2id(hash)
	if err != nil {
		return true
	}
	return p.memory < argonMemory || p.time < argonTime || p.threads < argonThreads
}

func GenerateToken(userID, tokenVersion int) (string, error) {
	now := time.Now()
	claims := &Claims{
		UserID:       userID,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(TokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(getJWTSecret())
}

func GenerateVerificationToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		// crypto/rand should never fail on Linux; if it does, panic.
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(bytes)
}

// AuditReference creates a stable, non-reversible reference for a value that an
// auth rate-limit needs to correlate (currently email addresses and IPs). Raw
// identifiers must not be copied into auth_audit: that table is included in
// backups and survives far longer than an in-memory limiter.
//
// The JWT secret is used as the HMAC key with a domain separator. A database or
// backup compromise alone therefore cannot dictionary-guess low-entropy values
// such as an email address or IPv4 address. Rotating JWT_SECRET intentionally
// changes these references (and resets the short auth windows) alongside
// revoking every session.
func AuditReference(kind, value string) string {
	mac := hmac.New(sha256.New, getJWTSecret())
	_, _ = mac.Write([]byte("onion-spider/auth-audit/v1\x00"))
	_, _ = mac.Write([]byte(kind))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	return "hmac-sha256:v1:" + hex.EncodeToString(mac.Sum(nil))
}

// ValidateToken parses and validates a JWT, rejecting any algorithm other than HS256.
// Prevents the "alg=none" attack and algorithm substitution.
func ValidateToken(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return getJWTSecret(), nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
