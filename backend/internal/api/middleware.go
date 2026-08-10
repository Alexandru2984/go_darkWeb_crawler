package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"onion-spider/internal/auth"
	"onion-spider/internal/database"
)

type contextKey string

const (
	userContextKey       contextKey = "user"
	dbRoleContextKey     contextKey = "db_role"
	dbEmailContextKey    contextKey = "db_email"
	authSourceContextKey contextKey = "auth_source"
	networkContextKey    contextKey = "client_network"
)

// How a request proved its identity. CSRFProtect keys off this: only
// cookie-borne credentials can be attached by the browser automatically, so
// only they need the double-submit check.
const (
	authSourceHeader = "header"
	authSourceCookie = "cookie"
)

// JWTMiddleware extracts claims from the Authorization header, or failing that
// from the session cookie.
// Neither present: pass-through (public endpoints work).
// Present but invalid: 401 (do not allow forged tokens to pass as unauthenticated).
func JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The explicit header wins over the cookie. A caller who bothered to
		// set Authorization means it, and honouring the ambient cookie instead
		// would silently run the request as whoever the browser is logged in
		// as rather than as the presented token.
		tokenStr, source := "", ""
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr, source = strings.TrimPrefix(authHeader, "Bearer "), authSourceHeader
		} else if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			tokenStr, source = c.Value, authSourceCookie
		}
		if tokenStr == "" {
			next.ServeHTTP(w, r)
			return
		}

		claims, err := auth.ValidateToken(tokenStr)
		if err != nil {
			// A stale cookie is the ordinary end of a session, not an attack:
			// clear it so the browser stops replaying a token that will keep
			// failing, and the user lands back on the login form.
			if source == authSourceCookie {
				clearSessionCookies(w, r)
			}
			WriteJSONError(w, http.StatusUnauthorized, "Invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, claims)
		ctx = context.WithValue(ctx, authSourceContextKey, source)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AuthSource reports whether credentials arrived in a header or a cookie.
// Empty when the request is unauthenticated.
func AuthSource(r *http.Request) string {
	s, _ := r.Context().Value(authSourceContextKey).(string)
	return s
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

// LoadDBRole reads the current role and token_version from the DB and stores
// the role in the request context. It serves three purposes, all bypassing the
// (cacheable, 4h-lived) JWT claims:
//   - an admin demoted via SQL UPDATE loses privileges on the next request;
//   - a token whose version is stale (revoked via password reset / logout-all)
//     is rejected with 401;
//   - a token for a since-deleted user is rejected with 401.
//
// The role from JWT claims is NEVER used for authz.
func LoadDBRole(db *database.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid := GetUserID(r)
			if uid == 0 {
				next.ServeHTTP(w, r)
				return
			}
			email, role, tokenVersion, found, err := db.GetUserAuthInfo(uid)
			if err != nil {
				slog.ErrorContext(r.Context(), "load_db_role_failed", "uid", uid, "err", err)
				WriteJSONError(w, http.StatusInternalServerError, "Internal error")
				return
			}
			if !found {
				WriteJSONError(w, http.StatusUnauthorized, "Session no longer valid")
				return
			}
			if claims, ok := r.Context().Value(userContextKey).(*auth.Claims); ok && claims.TokenVersion != tokenVersion {
				WriteJSONError(w, http.StatusUnauthorized, "Session has been revoked, please log in again")
				return
			}
			ctx := context.WithValue(r.Context(), dbRoleContextKey, role)
			ctx = context.WithValue(ctx, dbEmailContextKey, email)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdminDB blocks requests whose DB-loaded role (set by LoadDBRole) is
// not 'admin'. MUST be preceded by LoadDBRole in the middleware chain.
//
// When requireMFA is set it additionally refuses administrators who have not
// enrolled a second factor. The gate is on administrative endpoints only: the
// account still works, and the enrolment endpoints are deliberately outside
// this middleware, so the way to satisfy the requirement is always reachable.
func RequireAdminDB(db *database.DB, requireMFA bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if GetUserID(r) == 0 {
				WriteJSONError(w, http.StatusUnauthorized, "Authentication required")
				return
			}
			if !IsAdmin(r) {
				WriteJSONError(w, http.StatusForbidden, "Admin role required")
				return
			}
			if requireMFA {
				state, err := db.GetTOTPState(GetUserID(r))
				if err != nil {
					slog.ErrorContext(r.Context(), "admin_mfa_check_failed", "uid", GetUserID(r), "err", err)
					WriteJSONError(w, http.StatusInternalServerError, "Internal error")
					return
				}
				if !state.Enabled {
					WriteJSONError(w, http.StatusForbidden,
						"Administrative actions require two-factor authentication. Enrol at /api/auth/totp/setup.")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
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

// GetDBEmail returns the live account email loaded alongside authorization
// state. Email is intentionally absent from the portable JWT credential.
func GetDBEmail(r *http.Request) string {
	email, _ := r.Context().Value(dbEmailContextKey).(string)
	return email
}
