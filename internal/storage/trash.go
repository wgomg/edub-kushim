package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

func TrashDir(storageDir string) string {
	return filepath.Join(storageDir, "trash")
}

func DocumentTrashDir(storageDir, documentID string) string {
	return filepath.Join(TrashDir(storageDir), documentID)
}

func MoveToTrash(storageDir, documentID, originalPath, storagePath, thumbnailPath string) (newOriginal, newStorage string, err error) {
	docTrash := DocumentTrashDir(storageDir, documentID)
	originalsDir := filepath.Join(docTrash, "originals")
	processedDir := filepath.Join(docTrash, "processed")

	if err := os.MkdirAll(originalsDir, 0755); err != nil {
		return "", "", fmt.Errorf("create trash originals dir: %w", err)
	}
	if err := os.MkdirAll(processedDir, 0755); err != nil {
		return "", "", fmt.Errorf("create trash processed dir: %w", err)
	}

	newOriginal = filepath.Join(originalsDir, filepath.Base(originalPath))
	newStorage = filepath.Join(processedDir, filepath.Base(storagePath))

	if err := moveFileIfExists(originalPath, newOriginal); err != nil {
		return "", "", err
	}
	if err := moveFileIfExists(storagePath, newStorage); err != nil {
		return "", "", err
	}

	if thumbnailPath != "" {
		thumbDir := filepath.Join(docTrash, "thumbnails")
		if err := os.MkdirAll(thumbDir, 0755); err != nil {
			return "", "", fmt.Errorf("create trash thumbnails dir: %w", err)
		}
		if err := moveFileIfExists(thumbnailPath, filepath.Join(thumbDir, filepath.Base(thumbnailPath))); err != nil {
			return "", "", err
		}
	}

	return newOriginal, newStorage, nil
}

func RestoreFromTrash(storageDir, documentID, trashOriginalPath, trashStoragePath, thumbnailPath string) (newOriginal, newStorage string, err error) {
	newOriginal = filepath.Join(storageDir, "originals", filepath.Base(trashOriginalPath))
	newStorage = filepath.Join(storageDir, "processed", filepath.Base(trashStoragePath))

	// Skip missing files — partial restore is better than failing entirely,
	// matching MoveToTrash's tolerance for absent source files.
	if _, statErr := os.Stat(trashOriginalPath); statErr == nil {
		if renameErr := os.Rename(trashOriginalPath, newOriginal); renameErr != nil {
			return "", "", fmt.Errorf("restore original file: %w", renameErr)
		}
	} else if !os.IsNotExist(statErr) {
		return "", "", fmt.Errorf("stat original file: %w", statErr)
	}

	if _, statErr := os.Stat(trashStoragePath); statErr == nil {
		if renameErr := os.Rename(trashStoragePath, newStorage); renameErr != nil {
			return "", "", fmt.Errorf("restore storage file: %w", renameErr)
		}
	} else if !os.IsNotExist(statErr) {
		return "", "", fmt.Errorf("stat storage file: %w", statErr)
	}

	if thumbnailPath != "" {
		trashThumb := filepath.Join(DocumentTrashDir(storageDir, documentID), "thumbnails", filepath.Base(thumbnailPath))
		if _, statErr := os.Stat(trashThumb); statErr == nil {
			if mkdirErr := os.MkdirAll(filepath.Dir(thumbnailPath), 0755); mkdirErr != nil {
				return "", "", fmt.Errorf("create thumbnail dir: %w", mkdirErr)
			}
			if renameErr := os.Rename(trashThumb, thumbnailPath); renameErr != nil {
				return "", "", fmt.Errorf("restore thumbnail file: %w", renameErr)
			}
		} else if !os.IsNotExist(statErr) {
			return "", "", fmt.Errorf("stat thumbnail file: %w", statErr)
		}
	}

	removeEmptyTrashDirs(storageDir, documentID)
	return newOriginal, newStorage, nil
}

func RemoveFromTrash(storageDir, documentID string) error {
	if err := os.RemoveAll(DocumentTrashDir(storageDir, documentID)); err != nil {
		return fmt.Errorf("remove trash dir: %w", err)
	}
	return nil
}

func moveFileIfExists(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("move %s to trash: %w", src, err)
	}
	return nil
}

func removeEmptyTrashDirs(storageDir, documentID string) {
	docTrash := DocumentTrashDir(storageDir, documentID)
	os.Remove(filepath.Join(docTrash, "thumbnails"))
	os.Remove(filepath.Join(docTrash, "originals"))
	os.Remove(filepath.Join(docTrash, "processed"))
	os.Remove(docTrash)
}
