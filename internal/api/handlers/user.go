package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/service"
)

// UserHandler is the user API: CRUD over the accounts.
type UserHandler struct {
	services *service.CrudServices
	logger   *log.Logger
}

// NewUserHandler wires the user handler to the services.
func NewUserHandler(services *service.CrudServices, logger *log.Logger) *UserHandler {
	return &UserHandler{services: services, logger: logger}
}

func toUserResponse(user database.User) UserResponse {
	created := ""
	if user.CreatedAt.Valid {
		created = user.CreatedAt.Time.Format(time.RFC3339)
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
	return UserResponse{
		ID:              user.ID,
		Username:        user.Username,
		Role:            user.Role,
		CreatedAt:       created,
		HasAPIKey:       hasKey,
		APIKeyPrefix:    prefix,
		APIKeyCreatedAt: createdAt,
	}
}

func toUserResponseFromRow(row database.ListUsersRow) UserResponse {
	created := ""
	if row.CreatedAt.Valid {
		created = row.CreatedAt.Time.Format(time.RFC3339)
	}
	var prefix *string
	if row.ApiKeyPrefix.Valid {
		p := row.ApiKeyPrefix.String
		prefix = &p
	}
	var createdAt *string
	if row.ApiKeyCreatedAt.Valid {
		t := row.ApiKeyCreatedAt.Time.Format(time.RFC3339)
		createdAt = &t
	}
	return UserResponse{
		ID:              row.ID,
		Username:        row.Username,
		Role:            row.Role,
		CreatedAt:       created,
		HasAPIKey:       prefix != nil,
		APIKeyPrefix:    prefix,
		APIKeyCreatedAt: createdAt,
	}
}

// List serves the paged user listing.
func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	pb := GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 100000)

	users, err := h.services.User.List(ctx, int32(limit), int32(offset))
	if err != nil {
		writeServiceError(w, h.logger, reqID, "list users", err)
		return
	}

	total, err := h.services.User.Count(ctx)
	if err != nil {
		writeServiceError(w, h.logger, reqID, "list users", err)
		return
	}

	responses := make([]UserResponse, len(users))
	for i, u := range users {
		responses[i] = toUserResponseFromRow(u)
	}

	writeJSON(w, http.StatusOK, UserListResponse{Users: responses, Total: total})
}

// Get serves one user without the hash.
func (h *UserHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	user, err := h.services.User.Get(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		writeServiceError(w, h.logger, reqID, "get user", err)
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// Create creates one user from the JSON body.
func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Username = stripTags(strings.TrimSpace(req.Username))
	if req.Username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	user, err := h.services.User.Create(ctx, req.Username, req.Password, req.Role)
	if err != nil {
		if errs.KindOf(err) == errs.KindConflict {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "username already exists"})
			return
		}
		writeServiceError(w, h.logger, reqID, "create user", err)
		return
	}

	writeJSON(w, http.StatusCreated, toUserResponse(user))
}

// Update changes one user's username, password and role.
func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Username = stripTags(strings.TrimSpace(req.Username))
	if req.Username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	user, err := h.services.User.Update(ctx, id, req.Username, req.Password, req.Role)
	if err != nil {
		if errs.KindOf(err) == errs.KindConflict {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "username already exists"})
			return
		}
		writeServiceError(w, h.logger, reqID, "update user", err)
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(user))
}

// Delete removes one user.
func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	existing, err := h.services.User.Get(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		writeServiceError(w, h.logger, reqID, "delete user", err)
		return
	}

	if err := h.services.User.Delete(ctx, existing.ID); err != nil {
		writeServiceError(w, h.logger, reqID, "delete user", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// CreateUserRequest is the body of POST /api/v1/users; an empty role
// defaults to viewer.
type CreateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role,omitempty"`
}

// UpdateUserRequest is the body of a user update; an empty password
// keeps the current one.
type UpdateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Role     string `json:"role,omitempty"`
}

// UserResponse is a user as the API shows it: no hash, but the API key's
// existence, prefix and creation time.
type UserResponse struct {
	ID              int64   `json:"id"`
	Username        string  `json:"username"`
	Role            string  `json:"role"`
	CreatedAt       string  `json:"created_at"`
	HasAPIKey       bool    `json:"has_api_key"`
	APIKeyPrefix    *string `json:"api_key_prefix,omitempty"`
	APIKeyCreatedAt *string `json:"api_key_created_at,omitempty"`
}

// UserListResponse is one page of users.
type UserListResponse struct {
	Users []UserResponse `json:"users"`
	Total int64          `json:"total"`
}

// CreateAPIKeyResponse returns the freshly minted API key, which the
// client must store because only its hash is kept server-side.
type CreateAPIKeyResponse struct {
	APIKey  string `json:"api_key"`
	Prefix  string `json:"prefix"`
	Message string `json:"message"`
}

// APIKeyStatusResponse says whether a user has an API key and its
// non-secret metadata.
type APIKeyStatusResponse struct {
	HasAPIKey       bool    `json:"has_api_key"`
	APIKeyPrefix    *string `json:"api_key_prefix,omitempty"`
	APIKeyCreatedAt *string `json:"api_key_created_at,omitempty"`
}
