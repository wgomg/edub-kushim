package database

// ToDocument converts the row to the full Document model. Used where the
// model type is expected (e.g. enrichment), since sqlc generates a dedicated
// row type for queries selecting explicit columns.
func (r GetDocumentRow) ToDocument() Document {
	return Document{
		ID:               r.ID,
		DocumentID:       r.DocumentID,
		Title:            r.Title,
		Md5Checksum:      r.Md5Checksum,
		Sha512Checksum:   r.Sha512Checksum,
		OriginalType:     r.OriginalType,
		FileSize:         r.FileSize,
		PageCount:        r.PageCount,
		WordCount:        r.WordCount,
		CharCount:        r.CharCount,
		Language:         r.Language,
		CreatedAt:        r.CreatedAt,
		ModifiedAt:       r.ModifiedAt,
		DocumentTypeID:   r.DocumentTypeID,
		OriginalPath:     r.OriginalPath,
		StoragePath:      r.StoragePath,
		TextContent:      r.TextContent,
		TextSearchVector: r.TextSearchVector,
	}
}
