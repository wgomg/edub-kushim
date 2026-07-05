package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type APIKeyHandler struct {
	userService *service.User
	logger      *utils.Logger
}

func NewAPIKeyHandler(userService *service.User, logger *utils.Logger) *APIKeyHandler {
	return &APIKeyHandler{userService: userService, logger: logger}
}

func (h *APIKeyHandler) parseID(r *http.Request) (int64, error) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (h *APIKeyHandler) authorize(r *http.Request, targetID int64) bool {
	callerID, _ := r.Context().Value(auth.UserIDKey).(int64)
	return callerID == targetID
}

func (h *APIKeyHandler) issueKey(w http.ResponseWriter, r *http.Request, keyFunc func(context.Context, int64) (string, error), message string, status int) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	id, err := h.parseID(r)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	if !h.authorize(r, id) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	rawKey, err := keyFunc(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		writeServiceError(w, h.logger, &reqID, "api key operation", err)
		return
	}

	prefix := rawKey[:len("ek_")+9]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(types.CreateAPIKeyResponse{
		APIKey:  rawKey,
		Prefix:  prefix,
		Message: message,
	})
}

func (h *APIKeyHandler) GenerateKey(w http.ResponseWriter, r *http.Request) {
	h.issueKey(w, r, h.userService.CreateAPIKey, "Save this API key — it will not be shown again", http.StatusCreated)
}

func (h *APIKeyHandler) RotateKey(w http.ResponseWriter, r *http.Request) {
	h.issueKey(w, r, h.userService.RotateAPIKey, "Previous API key has been revoked. Save this new key — it will not be shown again", http.StatusOK)
}

func (h *APIKeyHandler) RevokeKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	id, err := h.parseID(r)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	if !h.authorize(r, id) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := h.userService.RevokeAPIKey(ctx, id); err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		writeServiceError(w, h.logger, &reqID, "revoke api key", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *APIKeyHandler) GetKeyStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	id, err := h.parseID(r)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	if !h.authorize(r, id) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	user, err := h.userService.Get(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		writeServiceError(w, h.logger, &reqID, "get api key status", err)
		return
	}

	hasKey := user.ApiKeyHash.Valid
	var prefix *string
	if user.ApiKeyPrefix.Valid {
		p := user.ApiKeyPrefix.String
		prefix = &p
	}
	var createdAt *string
	if user.ApiKeyCreatedAt.Valid {
		t := user.ApiKeyCreatedAt.Time.Format(time.RFC3339)
		createdAt = &t
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(types.APIKeyStatusResponse{
		HasAPIKey:       hasKey,
		APIKeyPrefix:    prefix,
		APIKeyCreatedAt: createdAt,
	})
}

func (h *APIKeyHandler) callerID(r *http.Request) int64 {
	id, _ := r.Context().Value(auth.UserIDKey).(int64)
	return id
}

func (h *APIKeyHandler) MeGenerateKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	id := h.callerID(r)
	if id == 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rawKey, err := h.userService.CreateAPIKey(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		writeServiceError(w, h.logger, &reqID, "generate api key", err)
		return
	}

	prefix := rawKey[:len("ek_")+9]
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(types.CreateAPIKeyResponse{
		APIKey:  rawKey,
		Prefix:  prefix,
		Message: "Save this API key — it will not be shown again",
	})
}

func (h *APIKeyHandler) MeRevokeKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	id := h.callerID(r)
	if id == 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := h.userService.RevokeAPIKey(ctx, id); err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		writeServiceError(w, h.logger, &reqID, "revoke api key", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *APIKeyHandler) MeRotateKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	id := h.callerID(r)
	if id == 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rawKey, err := h.userService.RotateAPIKey(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		writeServiceError(w, h.logger, &reqID, "rotate api key", err)
		return
	}

	prefix := rawKey[:len("ek_")+9]
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(types.CreateAPIKeyResponse{
		APIKey:  rawKey,
		Prefix:  prefix,
		Message: "Previous API key has been revoked. Save this new key — it will not be shown again",
	})
}

func (h *APIKeyHandler) MeGetKeyStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)
	id := h.callerID(r)
	if id == 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := h.userService.Get(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		writeServiceError(w, h.logger, &reqID, "get api key status", err)
		return
	}

	hasKey := user.ApiKeyHash.Valid
	var prefix *string
	if user.ApiKeyPrefix.Valid {
		p := user.ApiKeyPrefix.String
		prefix = &p
	}
	var createdAt *string
	if user.ApiKeyCreatedAt.Valid {
		t := user.ApiKeyCreatedAt.Time.Format(time.RFC3339)
		createdAt = &t
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(types.APIKeyStatusResponse{
		HasAPIKey:       hasKey,
		APIKeyPrefix:    prefix,
		APIKeyCreatedAt: createdAt,
	})
}
