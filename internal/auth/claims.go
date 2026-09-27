// Package auth is the identity vocabulary of the HTTP layer: the roles,
// the JWT session tokens and their claims, the API-key generation and
// hashing, and the context keys the middleware installs.
package auth

import "github.com/golang-jwt/jwt/v5"

// Role is an account role; admin, editor and viewer are the only
// recognized values.
type Role string

// The roles, from most to least privileged.
const (
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

// ValidRole reports whether the string names a known role.
func ValidRole(r string) bool {
	switch Role(r) {
	case RoleAdmin, RoleEditor, RoleViewer:
		return true
	}
	return false
}

// Claims is the JWT payload: the user identity plus the registered
// expiry and issuance.
type Claims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     Role   `json:"role"`
	jwt.RegisteredClaims
}

type contextKey string

// The request-context keys the auth middleware installs.
const (
	RoleKey       contextKey = "role"
	UserIDKey     contextKey = "userID"
	UsernameKey   contextKey = "username"
	AuthSourceKey contextKey = "authSource"
)
