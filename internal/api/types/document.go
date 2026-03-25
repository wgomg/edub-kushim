package types

type CreateDocumentRequest struct {
	Title          string `json:"title"`
	MD5Checksum    string `json:"md5_checksum"`
	SHA512Checksum string `json:"sha512_checksum"`
	Filename       string `json:"filename"`
	MimeType       string `json:"mime_type"`
	FileSize       int64  `json:"file_size"`
	SourcePath     string `json:"source_path"`
}

type DocumentResponse struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	MD5Checksum    string `json:"md5_checksum"`
	SHA512Checksum string `json:"sha512_checksum"`
	Filename       string `json:"filename"`
	MimeType       string `json:"mime_type"`
	FileSize       int64  `json:"file_size"`
	CreatedAt      string `json:"created_at"`
	ModifiedAt     string `json:"modified_at"`
	SourcePath     string `json:"source_path"`
}
