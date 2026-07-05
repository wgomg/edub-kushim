package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	itypes "github.com/wgomg/edub-kushim/internal"
	"github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type UserHandler struct {
	services *itypes.CrudServices
	logger   *utils.Logger
}

func NewUserHandler(services *itypes.CrudServices, logger *utils.Logger) *UserHandler {
	return &UserHandler{services: services, logger: logger}
}

func toUserResponse(user database.User) types.UserResponse {
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
	return types.UserResponse{
		ID:              user.ID,
		Username:        user.Username,
		CreatedAt:       created,
		HasAPIKey:       hasKey,
		APIKeyPrefix:    prefix,
		APIKeyCreatedAt: createdAt,
	}
}

func toUserResponseFromRow(row database.ListUsersRow) types.UserResponse {
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
	return types.UserResponse{
		ID:              row.ID,
		Username:        row.Username,
		CreatedAt:       created,
		HasAPIKey:       prefix != nil,
		APIKeyPrefix:    prefix,
		APIKeyCreatedAt: createdAt,
	}
}

func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	pb := utils.GetParamBag(r)
	if pb == nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	limit := pb.GetInt64("limit", 50, 1, 100)
	offset := pb.GetInt64("offset", 0, 0, 100000)

	users, err := h.services.User.List(ctx, limit, offset)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "list users", err)
		return
	}

	total, err := h.services.User.Count(ctx)
	if err != nil {
		writeServiceError(w, h.logger, &reqID, "list users", err)
		return
	}

	responses := make([]types.UserResponse, len(users))
	for i, u := range users {
		responses[i] = toUserResponseFromRow(u)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(types.UserListResponse{Users: responses, Total: total})
}

func (h *UserHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

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
		writeServiceError(w, h.logger, &reqID, "get user", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toUserResponse(user))
}

func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	var req types.CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Username = utils.StripTags(strings.TrimSpace(req.Username))
	if req.Username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	user, err := h.services.User.Create(ctx, req.Username, req.Password)
	if err != nil {
		if errs.KindOf(err) == errs.KindConflict {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "username already exists"})
			return
		}
		writeServiceError(w, h.logger, &reqID, "create user", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toUserResponse(user))
}

func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
	if err != nil {
		http.Error(w, "Invalid id", http.StatusBadRequest)
		return
	}

	var req types.UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.Username = utils.StripTags(strings.TrimSpace(req.Username))
	if req.Username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	user, err := h.services.User.Update(ctx, id, req.Username, req.Password)
	if err != nil {
		if errs.KindOf(err) == errs.KindConflict {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "username already exists"})
			return
		}
		writeServiceError(w, h.logger, &reqID, "update user", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toUserResponse(user))
}

func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := ctx.Value("reqid").(string)

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
		writeServiceError(w, h.logger, &reqID, "delete user", err)
		return
	}

	if err := h.services.User.Delete(ctx, existing.ID); err != nil {
		writeServiceError(w, h.logger, &reqID, "delete user", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
