package types

type PersonRefResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type DocumentTypeRefResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type PeopleTypeRefResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
