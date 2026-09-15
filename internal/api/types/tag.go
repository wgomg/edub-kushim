package types

import "time"

type TagListResponse struct {
	Results []TagResponse `json:"results"`
	Total   int64         `json:"total"`
}

type DeletedTagResponse struct {
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	TargetName string    `json:"target_name,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type DeletedTagListResponse struct {
	Results []DeletedTagResponse `json:"results"`
	Total   int64                `json:"total"`
}

type CreateTagRequest struct {
	Name string `json:"name"`
}

type UpdateTagRequest struct {
	Name string `json:"name"`
}

type MergeTagRequest struct {
	TargetID int64 `json:"target_id"`
}

type MergeTagResponse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Reassigned int64  `json:"reassigned"`
}
