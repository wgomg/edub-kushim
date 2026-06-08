package types

type TagResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type PersonResponse struct {
	ID                    int64  `json:"id"`
	Name                  string `json:"name"`
	PersonTypeID          int64  `json:"person_type_id"`
	PersonTypeName        string `json:"person_type_name"`
	PersonTypeDescription string `json:"person_type_description"`
}

type DocumentResponse struct {
	ID               int64            `json:"id"`
	Title            string           `json:"title"`
	MD5Checksum      string           `json:"md5_checksum"`
	SHA512Checksum   string           `json:"sha512_checksum"`
	MimeType         string           `json:"mime_type"`
	FileSize         int64            `json:"file_size"`
	Language         string           `json:"language"`
	DocumentTypeID   *int64           `json:"document_type_id,omitempty"`
	DocumentTypeName *string          `json:"document_type_name,omitempty"`
	Tags             []TagResponse    `json:"tags,omitempty"`
	People           []PersonResponse `json:"people,omitempty"`
	CreatedAt        string           `json:"created_at"`
	ModifiedAt       string           `json:"modified_at"`
}

type FTSDocumentResponse struct {
	ID               int64            `json:"id"`
	Title            string           `json:"title"`
	MD5Checksum      string           `json:"md5_checksum"`
	SHA512Checksum   string           `json:"sha512_checksum"`
	MimeType         string           `json:"mime_type"`
	FileSize         int64            `json:"file_size"`
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
