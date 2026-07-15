package types

import (
	"encoding/json"
	"time"
)

type CreateSavedSearchRequest struct {
	Name   string          `json:"name"`
	Filter json.RawMessage `json:"filter"`
}

type SavedSearchResponse struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Filter    json.RawMessage `json:"filter"`
	CreatedAt time.Time       `json:"created_at"`
}
