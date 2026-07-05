package handlers

import (
	"encoding/json"
	"net/http"

	types "github.com/wgomg/edub-kushim/internal/api/types"
	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type AuthHandler struct {
	userService *service.User
	getConfig   func() *config.Config
	logger      *utils.Logger
}

func NewAuthHandler(userService *service.User, getConfig func() *config.Config, logger *utils.Logger) *AuthHandler {
	return &AuthHandler{
		userService: userService,
		getConfig:   getConfig,
		logger:      logger,
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token string              `json:"token"`
	User  types.UserResponse   `json:"user"`
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": "invalid username or password"})
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Username == "" || req.Password == "" {
		writeUnauthorized(w)
		return
	}

	user, err := h.userService.Authenticate(r.Context(), req.Username, req.Password)
	if err != nil {
		kind := errs.KindOf(err)
		if kind == errs.KindNotFound || kind == errs.KindInvalid {
			writeUnauthorized(w)
			return
		}
		reqID := r.Context().Value("reqid")
		if reqIDStr, ok := reqID.(string); ok {
			h.logger.Error(&reqIDStr, "auth login: %v", err)
		} else {
			h.logger.Error(nil, "auth login: %v", err)
		}
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	token, err := auth.GenerateToken(user.ID, user.Username, h.getConfig().Srv.SessionSecret)
	if err != nil {
		h.logger.Error(nil, "generate token: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	resp := loginResponse{
		Token: token,
		User:  toUserResponse(user),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}


