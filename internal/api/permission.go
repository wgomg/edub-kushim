package api

import (
	"net/http"

	"github.com/wgomg/edub-kushim/internal/auth"
)

func RequireRole(allowed ...auth.Role) func(http.Handler) http.Handler {
	allowedSet := make(map[auth.Role]bool, len(allowed))
	for _, r := range allowed {
		allowedSet[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, _ := r.Context().Value(auth.RoleKey).(string)
			if role == "" || !allowedSet[auth.Role(role)] {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"forbidden"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}


