package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wgomg/edub-kushim/internal/auth"
)

const testSecret = "test-middleware-secret"

func authHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(auth.UserIDKey).(int64)
		username, _ := r.Context().Value(auth.UsernameKey).(string)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"user_id":  userID,
			"username": username,
		})
	})
}

func TestAuthMiddleware_PublicPaths_BypassAuth(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true })
	publicPaths := []string{
		"/health",
		"/wizard/config",
		"/wizard/config/status",
		"/api/v1/auth/login",
		"/api/v1/auth/logout",
		"/",
		"/documents",
		"/settings",
	}

	for _, path := range publicPaths {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		middleware.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("path %s: expected 200 for public path, got %d", path, w.Code)
		}
	}
}

func TestAuthMiddleware_ProtectedPath_NoToken(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true })
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ProtectedPath_InvalidToken(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true })
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer invalid.jwt.token")
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ProtectedPath_WrongSecret(t *testing.T) {
	token, _ := auth.GenerateToken(1, "user", "other-secret")
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true })
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ProtectedPath_ValidToken(t *testing.T) {
	token, err := auth.GenerateToken(42, "alice", testSecret)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true })
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		UserID   int64  `json:"user_id"`
		Username string `json:"username"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.UserID != 42 {
		t.Errorf("expected user_id 42, got %d", resp.UserID)
	}
	if resp.Username != "alice" {
		t.Errorf("expected username 'alice', got %q", resp.Username)
	}
}

func TestAuthMiddleware_MissingBearerPrefix(t *testing.T) {
	token, _ := auth.GenerateToken(1, "user", testSecret)
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true })
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", token)
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_EmptyAuthorizationHeader(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true })
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "")
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_Disabled_PassesAllRequests(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return false })
	protectedPaths := []string{
		"/api/v1/documents",
		"/api/v1/tags",
		"/api/v1/consume",
		"/api/v1/users",
	}

	for _, path := range protectedPaths {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		middleware.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("path %s: expected 200 when auth disabled, got %d", path, w.Code)
		}

		var resp struct {
			UserID   int64  `json:"user_id"`
			Username string `json:"username"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Errorf("path %s: failed to decode response: %v", path, err)
		}
	}
}
