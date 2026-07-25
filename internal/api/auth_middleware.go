package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
)

func AuthMiddleware(
	next http.Handler,
	getSecret func() string,
	getAuthEnabled func() bool,
	validateAPIKey func(ctx context.Context, rawKey string) (*database.User, error),
	getUserByID func(ctx context.Context, id int64) (*database.User, error),
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	if !getAuthEnabled() {
		ctx := context.WithValue(r.Context(), auth.RoleKey, string(auth.RoleAdmin))
		ctx = context.WithValue(ctx, auth.UserIDKey, int64(1))
		ctx = context.WithValue(ctx, auth.UsernameKey, "admin")
		next.ServeHTTP(w, r.WithContext(ctx))
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
		if authHeader == "" {
			if cookie, err := r.Cookie("edub_token"); err == nil {
				authHeader = "Bearer " + cookie.Value
			}
		}
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing or invalid authorization header"})
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		if strings.HasPrefix(tokenString, auth.APIKeyPrefix) {
			user, err := validateAPIKey(r.Context(), tokenString)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				if errs.KindOf(err) == errs.KindInternal {
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{"error": "internal server error"})
				} else {
					w.WriteHeader(http.StatusUnauthorized)
					json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired token"})
				}
				return
			}

			ctx := context.WithValue(r.Context(), auth.UserIDKey, user.ID)
			ctx = context.WithValue(ctx, auth.UsernameKey, user.Username)
			ctx = context.WithValue(ctx, auth.RoleKey, user.Role)
			ctx = context.WithValue(ctx, auth.AuthSourceKey, "apikey")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		claims, err := auth.ValidateToken(tokenString, getSecret())
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired token"})
			return
		}

		user, err := getUserByID(r.Context(), claims.UserID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			if errs.KindOf(err) == errs.KindNotFound {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid or expired token"})
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "internal server error"})
			}
			return
		}

		ctx := context.WithValue(r.Context(), auth.UserIDKey, user.ID)
		ctx = context.WithValue(ctx, auth.UsernameKey, user.Username)
		ctx = context.WithValue(ctx, auth.RoleKey, user.Role)
		ctx = context.WithValue(ctx, auth.AuthSourceKey, "session")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
