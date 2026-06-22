package types

type CreatePersonRequest struct {
	Name       string `json:"name"`
	NameNative string `json:"name_native"`
}

type UpdatePersonRequest struct {
	Name       string `json:"name"`
	NameNative string `json:"name_native"`
}

type CreatePeopleTypeRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type UpdatePeopleTypeRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type PeopleTypeResponse struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}
