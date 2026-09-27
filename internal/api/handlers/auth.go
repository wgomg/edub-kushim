package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/service"
)

// AuthHandler is the session API: login, logout, and whoami.
type AuthHandler struct {
	userService *service.User
	getConfig   func() *config.Config
	logger      *log.Logger
}

// NewAuthHandler wires the auth handler to the users and the dynamic
// config.
func NewAuthHandler(userService *service.User, getConfig func() *config.Config, logger *log.Logger) *AuthHandler {
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
	Token string       `json:"token"`
	User  UserResponse `json:"user"`
}

func writeUnauthorized(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
}

// Login verifies the credentials and sets the session cookie.
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
		reqID := log.ReqIDFrom(r.Context())
		h.logger.Error(reqID, "auth login: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	token, err := auth.GenerateToken(user.ID, user.Username, auth.Role(user.Role), h.getConfig().Srv.SessionSecret)
	if err != nil {
		h.logger.Error("", "generate token: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	resp := loginResponse{
		Token: token,
		User:  toUserResponse(user),
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "edub_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.getConfig().App.Env != config.Development,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})

	writeJSON(w, http.StatusOK, resp)
}

// Logout clears the session cookie.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "edub_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.getConfig().App.Env != config.Development,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   0,
	})
	w.WriteHeader(http.StatusNoContent)
}

// MeHandler serves the authenticated user's identity.
func (h *AuthHandler) MeHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reqID := log.ReqIDFrom(ctx)

	userID, _ := ctx.Value(auth.UserIDKey).(int64)
	if userID == 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := h.userService.Get(ctx, userID)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			http.Error(w, "User not found", http.StatusNotFound)
			return
		}
		writeServiceError(w, h.logger, reqID, "get me", err)
		return
	}

	writeJSON(w, http.StatusOK, toUserResponse(user))
}
