package storage

// Canonical storage subdirectory names. The string values are part of the
// persisted data contract (DB rows, existing backups) and must not change.
const (
	DirProcessed        = "processed"
	DirOriginal         = "originals"
	DirThumbnails       = "thumbnails"
	DirTrash            = "trash"
	DirOrphaned         = "orphaned"
	DirErrors           = "errors"
	DirErrorsDuplicates = "duplicated"
)
