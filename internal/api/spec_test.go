package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/service"
)

// openAPITestMux builds the real route table without a database: the constructors only store
// their dependencies, and the spec route never consults them.
func openAPITestMux() *http.ServeMux {
	return registerRoutes(
		log.NewDiscardLogger(),
		&database.Client{},
		nil,
		func() *config.Config { return &config.Config{} },
		func(*config.Config) {},
		&service.CrudServices{},
		nil,
		nil,
		nil,
	)
}

func TestServeOpenAPISpec_ServesEmbeddedSpecToViewer(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/openapi.yaml", nil)
	r = r.WithContext(context.WithValue(r.Context(), auth.RoleKey, "viewer"))
	w := httptest.NewRecorder()

	openAPITestMux().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for viewer, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "application/yaml" {
		t.Errorf("expected Content-Type application/yaml, got %q", got)
	}
	body := w.Body.String()
	if body != string(openAPISpec) {
		t.Errorf("body differs from the embedded spec: served %d bytes, embedded %d bytes", len(body), len(openAPISpec))
	}
}

func TestServeOpenAPISpec_ForbidsMissingRole(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/openapi.yaml", nil)
	w := httptest.NewRecorder()

	openAPITestMux().ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 without a role, got %d", w.Code)
	}
}
