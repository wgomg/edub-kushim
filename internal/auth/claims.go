package auth

import "github.com/golang-jwt/jwt/v5"

type Claims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type contextKey string

const UserIDKey contextKey = "userID"

const UsernameKey contextKey = "username"
