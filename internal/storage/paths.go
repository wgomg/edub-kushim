package storage

import (
	"fmt"
	"path/filepath"
	"strconv"
	"time"
)

func ThumbnailPath(storageDir string, createdAt time.Time, docID string) string {
	datePath := filepath.Join(
		strconv.Itoa(createdAt.Year()),
		fmt.Sprintf("%02d", createdAt.Month()),
		fmt.Sprintf("%02d", createdAt.Day()),
		fmt.Sprintf("%02d", createdAt.Hour()),
	)
	return filepath.Join(storageDir, "thumbnails", datePath, docID+".jpg")
}
