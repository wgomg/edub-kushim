package types

type CreateDocumentTypeRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type UpdateDocumentTypeRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type DocumentTypeResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
