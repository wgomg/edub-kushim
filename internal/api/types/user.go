package types

type CreateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type UpdateUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

type UserResponse struct {
	ID              int64   `json:"id"`
	Username        string  `json:"username"`
	CreatedAt       string  `json:"created_at"`
	HasAPIKey       bool    `json:"has_api_key"`
	APIKeyPrefix    *string `json:"api_key_prefix,omitempty"`
	APIKeyCreatedAt *string `json:"api_key_created_at,omitempty"`
}

type UserListResponse struct {
	Users []UserResponse `json:"users"`
	Total int64          `json:"total"`
}

type CreateAPIKeyResponse struct {
	APIKey  string `json:"api_key"`
	Prefix  string `json:"prefix"`
	Message string `json:"message"`
}

type APIKeyStatusResponse struct {
	HasAPIKey       bool    `json:"has_api_key"`
	APIKeyPrefix    *string `json:"api_key_prefix,omitempty"`
	APIKeyCreatedAt *string `json:"api_key_created_at,omitempty"`
}
