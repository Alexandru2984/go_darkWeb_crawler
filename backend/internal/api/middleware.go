package api

import (
	"context"
	"log"
	"net/http"
	"strings"

	"onion-spider/internal/auth"
	"onion-spider/internal/database"
)

type contextKey string

const (
	userContextKey   contextKey = "user"
	dbRoleContextKey contextKey = "db_role"
)

// JWTMiddleware extracts claims from the Authorization header if present and valid.
// No header: pass-through (public endpoints work).
// Header present but invalid: 401 (do not allow forged tokens to pass as unauthenticated).
func JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := auth.ValidateToken(tokenStr)
		if err != nil {
			WriteJSONError(w, http.StatusUnauthorized, "Invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth refuses requests without a valid JWT in context.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Value(userContextKey).(*auth.Claims); !ok {
			WriteJSONError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LoadDBRole reads the current role from the DB and stores it in the request
// context. Prevents an admin demoted via SQL UPDATE from retaining privileges
// until the JWT expires (4h). The role from JWT claims is NOT used for authz.
func LoadDBRole(db *database.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid := GetUserID(r)
			if uid == 0 {
				next.ServeHTTP(w, r)
				return
			}
			role, err := db.GetUserRole(uid)
			if err != nil {
				log.Printf("[ERROR] LoadDBRole uid=%d: %v", uid, err)
				WriteJSONError(w, http.StatusInternalServerError, "Internal error")
				return
			}
			ctx := context.WithValue(r.Context(), dbRoleContextKey, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdminDB blocks requests whose DB-loaded role (set by LoadDBRole) is
// not 'admin'. MUST be preceded by LoadDBRole in the middleware chain.
func RequireAdminDB(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetUserID(r) == 0 {
			WriteJSONError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		if !IsAdmin(r) {
			WriteJSONError(w, http.StatusForbidden, "Admin role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetUserID returns the authenticated user's ID, or 0 if no valid JWT was provided.
func GetUserID(r *http.Request) int {
	claims, ok := r.Context().Value(userContextKey).(*auth.Claims)
	if !ok || claims == nil {
		return 0
	}
	return claims.UserID
}

// IsAdmin reads the role from the DB (via LoadDBRole middleware), NOT from JWT claims.
// Ensures a demoted admin loses privileges immediately, without waiting for JWT expiry.
// Returns false if LoadDBRole did not run (public endpoint).
func IsAdmin(r *http.Request) bool {
	role, ok := r.Context().Value(dbRoleContextKey).(string)
	return ok && role == "admin"
}
