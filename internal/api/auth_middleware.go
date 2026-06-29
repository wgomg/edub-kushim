package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wgomg/edub-kushim/internal/auth"
)

func AuthMiddleware(next http.Handler, getSecret func() string, getAuthEnabled func() bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !getAuthEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		path := r.URL.Path

		if path == "/health" ||
			strings.HasPrefix(path, "/wizard/") ||
			strings.HasPrefix(path, "/api/v1/auth/") ||
			!strings.HasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing or invalid authorization header"})
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := auth.ValidateToken(tokenString, getSecret())
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired token"})
			return
		}

		ctx := context.WithValue(r.Context(), auth.UserIDKey, claims.UserID)
		ctx = context.WithValue(ctx, auth.UsernameKey, claims.Username)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
