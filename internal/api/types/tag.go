package types

type TagListResponse struct {
	Results []TagResponse `json:"results"`
	Total   int64         `json:"total"`
}

type CreateTagRequest struct {
	Name string `json:"name"`
}

type UpdateTagRequest struct {
	Name string `json:"name"`
}
