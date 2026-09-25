package handlers

// PersonRefResponse is a person id and name, the autocomplete shape.
type PersonRefResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// DocumentTypeRefResponse is a document-type autocomplete entry.
type DocumentTypeRefResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// PeopleTypeRefResponse is a people-type entry for the UI's selects.
type PeopleTypeRefResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
