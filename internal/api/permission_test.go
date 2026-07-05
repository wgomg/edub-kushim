package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wgomg/edub-kushim/internal/auth"
)

func permissionTestHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireRole_AllowsAdmin(t *testing.T) {
	handler := RequireRole(auth.RoleAdmin)(permissionTestHandler())
	r := httptest.NewRequest("GET", "/api/v1/test", nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.RoleKey, "admin"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for admin, got %d", w.Code)
	}
}

func TestRequireRole_AllowsEditor(t *testing.T) {
	handler := RequireRole(auth.RoleEditor, auth.RoleAdmin)(permissionTestHandler())
	r := httptest.NewRequest("GET", "/api/v1/test", nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.RoleKey, "editor"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for editor, got %d", w.Code)
	}
}

func TestRequireRole_AllowsViewer(t *testing.T) {
	handler := RequireRole(auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin)(permissionTestHandler())
	r := httptest.NewRequest("GET", "/api/v1/test", nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.RoleKey, "viewer"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for viewer, got %d", w.Code)
	}
}

func TestRequireRole_ForbidsViewerFromAdmin(t *testing.T) {
	handler := RequireRole(auth.RoleAdmin)(permissionTestHandler())
	r := httptest.NewRequest("GET", "/api/v1/test", nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.RoleKey, "viewer"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for viewer on admin route, got %d", w.Code)
	}
}

func TestRequireRole_ForbidsEditorFromAdmin(t *testing.T) {
	handler := RequireRole(auth.RoleAdmin)(permissionTestHandler())
	r := httptest.NewRequest("GET", "/api/v1/test", nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.RoleKey, "editor"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for editor on admin route, got %d", w.Code)
	}
}

func TestRequireRole_ForbidsViewerFromEditor(t *testing.T) {
	handler := RequireRole(auth.RoleEditor, auth.RoleAdmin)(permissionTestHandler())
	r := httptest.NewRequest("GET", "/api/v1/test", nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.RoleKey, "viewer"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for viewer on editor route, got %d", w.Code)
	}
}

func TestRequireRole_MissingRole(t *testing.T) {
	handler := RequireRole(auth.RoleAdmin)(permissionTestHandler())
	r := httptest.NewRequest("GET", "/api/v1/test", nil)
	// No role in context
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 when role missing, got %d", w.Code)
	}
}

func TestRequireRole_InvalidRole(t *testing.T) {
	handler := RequireRole(auth.RoleAdmin)(permissionTestHandler())
	r := httptest.NewRequest("GET", "/api/v1/test", nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.RoleKey, "superadmin"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for unknown role, got %d", w.Code)
	}
}
