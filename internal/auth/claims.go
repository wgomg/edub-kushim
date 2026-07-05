package auth

import "github.com/golang-jwt/jwt/v5"

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

const RoleKey contextKey = "role"

func ValidRole(r string) bool {
	switch Role(r) {
	case RoleAdmin, RoleEditor, RoleViewer:
		return true
	}
	return false
}

type Claims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

type contextKey string

const UserIDKey contextKey = "userID"

const UsernameKey contextKey = "username"

const AuthSourceKey contextKey = "authSource"
