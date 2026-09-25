package storage

import (
	"fmt"
	"path/filepath"
	"strconv"
	"time"
)

// ThumbnailPath builds the storage path for a document's thumbnail,
// bucketed by the creation time's year, month, day and hour.
func ThumbnailPath(storageDir string, createdAt time.Time, docID string) string {
	datePath := filepath.Join(
		strconv.Itoa(createdAt.Year()),
		fmt.Sprintf("%02d", createdAt.Month()),
		fmt.Sprintf("%02d", createdAt.Day()),
		fmt.Sprintf("%02d", createdAt.Hour()),
	)
	return filepath.Join(storageDir, DirThumbnails, datePath, docID+".jpg")
}
