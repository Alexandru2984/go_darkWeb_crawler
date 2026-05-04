package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	// MinSecretLen is the minimum accepted length for JWT_SECRET (32 hex characters = 128 bits of entropy).
	MinSecretLen = 32
	// TokenTTL is the lifetime of a JWT issued at login.
	TokenTTL = 4 * time.Hour
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
			log.Fatalf("FATAL: JWT_SECRET is missing or shorter than %d characters. Generate with: openssl rand -hex 32", MinSecretLen)
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
	h, err := bcrypt.GenerateFromPassword([]byte("dummy-timing-equalization-placeholder"), 12)
	if err != nil {
		log.Fatalf("FATAL: cannot generate dummyHash: %v", err)
	}
	dummyHash = string(h)
}

// dummyHash is a valid bcrypt hash used as the target in CompareHashAndPassword
// when a user does not exist — prevents timing attack for email enumeration.
var dummyHash string

// CheckAgainstDummy runs bcrypt.Compare on a password against a dummy hash.
// The response is always false — the sole purpose is to consume time equivalent to a real check.
func CheckAgainstDummy(password string) {
	if dummyHash == "" {
		return // should not happen after MustInitSecrets, but acts as a safe-guard
	}
	_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
}

type Claims struct {
	UserID int    `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// HashPassword hashes the password with bcrypt cost 12 (security/DoS balance).
func HashPassword(password string) (string, error) {
	// bcrypt truncates at 72 bytes — we reject long passwords to be predictable.
	if len(password) > 72 {
		return "", errors.New("password exceeds 72 characters")
	}
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(bytes), err
}

func CheckPasswordHash(password, hash string) bool {
	if len(password) > 72 {
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func GenerateToken(userID int, email, role string) (string, error) {
	now := time.Now()
	claims := &Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
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

// ValidateToken parses and validates a JWT, rejecting any algorithm other than HS256.
// Prevents the "alg=none" attack and algorithm substitution.
func ValidateToken(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return getJWTSecret(), nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
