package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// TrashDir returns the root directory that holds soft-deleted document files.
func TrashDir(storageDir string) string {
	return filepath.Join(storageDir, "trash")
}

// DocumentTrashDir returns the per-document trash directory. Mirrors the
// originals/processed layout of the main storage dir so basenames survive
// soft-delete and restore can reconstruct the original paths.
func DocumentTrashDir(storageDir, documentID string) string {
	return filepath.Join(TrashDir(storageDir), documentID)
}

// MoveToTrash moves a document's original and processed files under
// <trash>/<document_id>/. Missing source files are skipped so a soft-delete
// still succeeds when a file was manually removed. Returns the new paths.
func MoveToTrash(storageDir, documentID, originalPath, storagePath string) (newOriginal, newStorage string, err error) {
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

	return newOriginal, newStorage, nil
}

// RestoreFromTrash moves a document's files back to the main storage dir,
// reconstructing the original paths from the basenames. Returns the restored
// paths.
func RestoreFromTrash(storageDir, documentID, trashOriginalPath, trashStoragePath string) (newOriginal, newStorage string, err error) {
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

	removeEmptyTrashDirs(storageDir, documentID)
	return newOriginal, newStorage, nil
}

// RemoveFromTrash permanently removes a document's trash directory.
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
	// best-effort: subdirs are only empty when the document had no files there
	os.Remove(filepath.Join(docTrash, "originals"))
	os.Remove(filepath.Join(docTrash, "processed"))
	os.Remove(docTrash)
}
