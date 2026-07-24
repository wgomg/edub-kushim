package types

type PersonListResponse struct {
	Results []PersonResponse `json:"results"`
	Total   int64            `json:"total"`
}

type TagResponse struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	DocumentCount int64  `json:"document_count"`
}

type PersonResponse struct {
	ID                    int64  `json:"id"`
	Name                  string `json:"name"`
	NameNative            string `json:"name_native,omitempty"`
	PersonTypeID          int64  `json:"person_type_id"`
	PersonTypeName        string `json:"person_type_name"`
	PersonTypeDescription string `json:"person_type_description"`
	DocumentCount         int64  `json:"document_count"`
}

type DocumentResponse struct {
	ID               string           `json:"id"`
	Title            string           `json:"title"`
	MD5Checksum      string           `json:"md5_checksum"`
	SHA512Checksum   string           `json:"sha512_checksum"`
	MimeType         string           `json:"mime_type"`
	FileSize         int64            `json:"file_size"`
	PageCount        int32            `json:"page_count"`
	WordCount        int32            `json:"word_count"`
	CharCount        int32            `json:"char_count"`
	Language         string           `json:"language"`
	DocumentTypeID   *int64           `json:"document_type_id,omitempty"`
	DocumentTypeName *string          `json:"document_type_name,omitempty"`
	Tags             []TagResponse    `json:"tags,omitempty"`
	People           []PersonResponse `json:"people,omitempty"`
	CreatedAt        string           `json:"created_at"`
	ModifiedAt       string           `json:"modified_at"`
}

type FTSDocumentResponse struct {
	ID               string           `json:"id"`
	Title            string           `json:"title"`
	MD5Checksum      string           `json:"md5_checksum"`
	SHA512Checksum   string           `json:"sha512_checksum"`
	MimeType         string           `json:"mime_type"`
	FileSize         int64            `json:"file_size"`
	PageCount        int32            `json:"page_count"`
	WordCount        int32            `json:"word_count"`
	CharCount        int32            `json:"char_count"`
	Language         string           `json:"language"`
	DocumentTypeID   *int64           `json:"document_type_id,omitempty"`
	DocumentTypeName *string          `json:"document_type_name,omitempty"`
	Tags             []TagResponse    `json:"tags,omitempty"`
	People           []PersonResponse `json:"people,omitempty"`
	CreatedAt        string           `json:"created_at"`
	ModifiedAt       string           `json:"modified_at"`
	Rank             float64          `json:"rank"`
	Snippet          string           `json:"snippet"`
	TextContent      string           `json:"text_content"`
}

type SearchResponse struct {
	Results []FTSDocumentResponse `json:"results"`
	Total   int64                 `json:"total"`
}

type DocumentUpdateRequest struct {
	Title          string  `json:"title"`
	DocumentTypeID int64   `json:"document_type_id"`
	Language       string  `json:"language"`
	TextContent    *string `json:"text_content,omitempty"`
}

type AddDocumentTagRequest struct {
	TagID int64 `json:"tag_id"`
}

type RemoveDocumentTagRequest struct {
	TagID int64 `json:"tag_id"`
}

type AddDocumentPeopleRequest struct {
	PeopleID     int64 `json:"people_id"`
	PeopleTypeID int64 `json:"people_type_id"`
}

type RemoveDocumentPeopleRequest struct {
	PeopleID     int64 `json:"people_id"`
	PeopleTypeID int64 `json:"people_type_id"`
}

type DocumentDownloadRequest struct {
	DocumentIDs []string `json:"document_ids"`
}

type BatchDeleteRequest struct {
	DocumentIDs []string `json:"document_ids"`
}

type BatchDeleteError struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type BatchDeleteResult struct {
	Deleted int                `json:"deleted"`
	Failed  []BatchDeleteError `json:"failed,omitempty"`
}

type BatchTagRequest struct {
	DocumentIDs []string `json:"document_ids"`
	TagIDs      []int64  `json:"tag_ids"`
	Mode        string   `json:"mode"`
}

type BatchTagError struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type BatchTagResult struct {
	Assigned int             `json:"assigned"`
	Failed   []BatchTagError `json:"failed,omitempty"`
}
