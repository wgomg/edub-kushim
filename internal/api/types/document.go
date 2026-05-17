package types

type DocumentResponse struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	MD5Checksum    string `json:"md5_checksum"`
	SHA512Checksum string `json:"sha512_checksum"`
	MimeType       string `json:"mime_type"`
	FileSize       int64  `json:"file_size"`
	CreatedAt      string `json:"created_at"`
	ModifiedAt     string `json:"modified_at"`
}

type FTSDocumentResponse struct {
	ID             int64   `json:"id"`
	Title          string  `json:"title"`
	MD5Checksum    string  `json:"md5_checksum"`
	SHA512Checksum string  `json:"sha512_checksum"`
	MimeType       string  `json:"mime_type"`
	FileSize       int64   `json:"file_size"`
	CreatedAt      string  `json:"created_at"`
	ModifiedAt     string  `json:"modified_at"`
	Rank           float64 `json:"rank"`
	Snippet        string  `json:"snippet"`
	TextContent    string  `json:"text_content"`
}
