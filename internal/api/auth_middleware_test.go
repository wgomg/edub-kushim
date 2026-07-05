package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
)

const testSecret = "test-middleware-secret"

var testUser = &database.User{
	ID:       99,
	Username: "apikey-user",
	Role:     "viewer",
}

func authHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(auth.UserIDKey).(int64)
		username, _ := r.Context().Value(auth.UsernameKey).(string)
		role, _ := r.Context().Value(auth.RoleKey).(string)
		authSource, _ := r.Context().Value(auth.AuthSourceKey).(string)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"user_id":     userID,
			"username":    username,
			"role":        role,
			"auth_source": authSource,
		})
	})
}

func mockValidateAPIKey(user *database.User, err error) func(ctx context.Context, rawKey string) (*database.User, error) {
	return func(ctx context.Context, rawKey string) (*database.User, error) {
		return user, err
	}
}

func mockGetUserByID(user *database.User, err error) func(ctx context.Context, id int64) (*database.User, error) {
	return func(ctx context.Context, id int64) (*database.User, error) {
		return user, err
	}
}

func TestAuthMiddleware_PublicPaths_BypassAuth(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true }, mockValidateAPIKey(nil, errors.New("should not be called")), mockGetUserByID(nil, errors.New("should not be called")))
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
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true }, mockValidateAPIKey(nil, errors.New("should not be called")), mockGetUserByID(nil, errors.New("should not be called")))
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ProtectedPath_InvalidToken(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true }, mockValidateAPIKey(nil, errors.New("should not be called")), mockGetUserByID(nil, errors.New("should not be called")))
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer invalid.jwt.token")
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ProtectedPath_WrongSecret(t *testing.T) {
	token, _ := auth.GenerateToken(1, "user", "viewer", "other-secret")
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true }, mockValidateAPIKey(nil, errors.New("should not be called")), mockGetUserByID(nil, errors.New("should not be called")))
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ProtectedPath_ValidToken(t *testing.T) {
	token, err := auth.GenerateToken(42, "alice", "viewer", testSecret)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	jwtUser := &database.User{ID: 42, Username: "alice", Role: "viewer"}
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true }, mockValidateAPIKey(nil, errors.New("should not be called")), mockGetUserByID(jwtUser, nil))
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		UserID     int64  `json:"user_id"`
		Username   string `json:"username"`
		Role       string `json:"role"`
		AuthSource string `json:"auth_source"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.UserID != 42 {
		t.Errorf("expected user_id 42, got %d", resp.UserID)
	}
	if resp.Username != "alice" {
		t.Errorf("expected username 'alice', got %q", resp.Username)
	}
	if resp.Role != "viewer" {
		t.Errorf("expected role 'viewer', got %q", resp.Role)
	}
	if resp.AuthSource != "session" {
		t.Errorf("expected auth_source 'session', got %q", resp.AuthSource)
	}
}

func TestAuthMiddleware_MissingBearerPrefix(t *testing.T) {
	token, _ := auth.GenerateToken(1, "user", "viewer", testSecret)
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true }, mockValidateAPIKey(nil, errors.New("should not be called")), mockGetUserByID(nil, errors.New("should not be called")))
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", token)
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_EmptyAuthorizationHeader(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true }, mockValidateAPIKey(nil, errors.New("should not be called")), mockGetUserByID(nil, errors.New("should not be called")))
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "")
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_Disabled_PassesAllRequests(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return false }, mockValidateAPIKey(nil, errors.New("should not be called")), mockGetUserByID(nil, errors.New("should not be called")))
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
			Role     string `json:"role"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Errorf("path %s: failed to decode response: %v", path, err)
		}
	}
}

func TestAuthMiddleware_ValidAPIKey(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true }, mockValidateAPIKey(testUser, nil), mockGetUserByID(nil, errors.New("should not be called")))
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer ek_abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		UserID     int64  `json:"user_id"`
		Username   string `json:"username"`
		Role       string `json:"role"`
		AuthSource string `json:"auth_source"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.UserID != 99 {
		t.Errorf("expected user_id 99, got %d", resp.UserID)
	}
	if resp.Username != "apikey-user" {
		t.Errorf("expected username 'apikey-user', got %q", resp.Username)
	}
	if resp.Role != "viewer" {
		t.Errorf("expected role 'viewer', got %q", resp.Role)
	}
	if resp.AuthSource != "apikey" {
		t.Errorf("expected auth_source 'apikey', got %q", resp.AuthSource)
	}
}

func TestAuthMiddleware_InvalidAPIKey(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true }, mockValidateAPIKey(nil, errs.ENotFound("validate api key", errors.New("invalid api key"))), mockGetUserByID(nil, errors.New("should not be called")))
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer ek_boguskey1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_APIKeyWrongPrefix_FallsThrough(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true }, mockValidateAPIKey(nil, errors.New("should not be called")), mockGetUserByID(nil, errors.New("should not be called")))
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer not_ek_prefix")
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_APIKeyAuthDisabled_Bypasses(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return false }, mockValidateAPIKey(nil, errors.New("should not be called")), mockGetUserByID(nil, errors.New("should not be called")))
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer ek_abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_APIKeyInternalError(t *testing.T) {
	middleware := AuthMiddleware(authHandler(), func() string { return testSecret }, func() bool { return true }, mockValidateAPIKey(nil, errs.EInternal("validate api key", errors.New("db connection failed"))), mockGetUserByID(nil, errors.New("should not be called")))
	r := httptest.NewRequest("GET", "/api/v1/documents", nil)
	r.Header.Set("Authorization", "Bearer ek_abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	w := httptest.NewRecorder()
	middleware.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for internal error, got %d", w.Code)
	}
}
