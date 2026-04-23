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
	// MinSecretLen este lungimea minima acceptata pentru JWT_SECRET (32 caractere hex = 128 biti entropie).
	MinSecretLen = 32
	// TokenTTL este durata de viata a unui JWT emis la login.
	TokenTTL = 4 * time.Hour
)

var (
	jwtSecretOnce sync.Once
	jwtSecret     []byte
)

// getJWTSecret incarca JWT_SECRET LAZY — dupa ce main.go a apelat godotenv.Load().
// Daca secretul lipseste sau e prea scurt, aplicatia refuza sa porneasca.
// NU exista fallback — un secret slab e echivalent cu lipsa de autentificare.
func getJWTSecret() []byte {
	jwtSecretOnce.Do(func() {
		s := os.Getenv("JWT_SECRET")
		if len(s) < MinSecretLen {
			log.Fatalf("FATAL: JWT_SECRET lipseste sau e mai scurt de %d caractere. Genereaza cu: openssl rand -hex 32", MinSecretLen)
		}
		jwtSecret = []byte(s)
	})
	return jwtSecret
}

// MustInitSecrets forteaza incarcarea JWT_SECRET la pornirea aplicatiei,
// dupa ce variabilele de mediu sunt disponibile. Daca secretul lipseste
// sau e prea scurt, log.Fatal se declanseaza aici, nu la primul login.
// Precalculeaza si dummyHash pentru a egaliza timpul la login cu email inexistent.
func MustInitSecrets() {
	_ = getJWTSecret()
	h, err := bcrypt.GenerateFromPassword([]byte("dummy-timing-equalization-placeholder"), 12)
	if err != nil {
		log.Fatalf("FATAL: nu pot genera dummyHash: %v", err)
	}
	dummyHash = string(h)
}

// dummyHash este un hash bcrypt valid folosit ca tinta in CompareHashAndPassword
// atunci cand un utilizator nu exista — previne timing attack pentru enumerare email.
var dummyHash string

// CheckAgainstDummy ruleaza bcrypt.Compare pe o parola fata de un hash dummy.
// Raspunsul e intotdeauna false — scopul e doar sa consume timpul echivalent unei verificari reale.
func CheckAgainstDummy(password string) {
	if dummyHash == "" {
		return // nu ar trebui sa se intample dupa MustInitSecrets, dar e safe-guard
	}
	_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
}

type Claims struct {
	UserID int    `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// HashPassword hasheaza parola cu bcrypt cost 12 (balans siguranta/DoS).
func HashPassword(password string) (string, error) {
	// bcrypt trunchiaza la 72 bytes — refuzam parolele lungi pentru a fi predictibili.
	if len(password) > 72 {
		return "", errors.New("parola depaseste 72 caractere")
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
		// crypto/rand nu ar trebui sa esueze niciodata pe Linux; daca esueaza, panic.
		panic("crypto/rand indisponibil: " + err.Error())
	}
	return hex.EncodeToString(bytes)
}

// ValidateToken parseaza si valideaza un JWT, refuzand orice alt algorithm decat HS256.
// Previne atacul "alg=none" si substitutia de algoritm.
func ValidateToken(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("signing method neasteptat: %v", t.Header["alg"])
		}
		return getJWTSecret(), nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("token invalid")
	}
	return claims, nil
}
