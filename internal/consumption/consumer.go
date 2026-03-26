package consumption

import (
	"context"
	"crypto/md5"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Consumer struct {
	config *config.Config
	logger *utils.Logger
	db     *sql.DB
}

type File struct {
	Name                 string
	OriginalPath         string
	OCRTmpPath           *string
	StorageProcessedPath *string
	StorageOriginalPath  *string
	MD5Checksum          string
	SHA512Checksum       string
	Text                 *string
	MimeType             string
	Date                 time.Time
	FileSize             int64
}

func NewConsumer(cfg *config.Config, logger *utils.Logger, db *sql.DB) *Consumer {
	return &Consumer{
		config: cfg,
		logger: logger,
		db:     db,
	}
}

func (c *Consumer) Consume(reqID *string) error {
	filesToConsume, err := GetFiles(
		c.config.Storage.ConsumptionDir,
		c.config.Consumer.SupportedFiles,
	)
	if err != nil {
		return fmt.Errorf("error reading consumption dir: %w", err)
	}

	if len(filesToConsume) == 0 {
		c.logger.Info(reqID, "no files found")
		return nil
	}

	c.logger.Info(reqID, "%d files found", len(filesToConsume))

	for _, file := range filesToConsume {
		resultFile, err := c.Process(file)
		if err != nil {
			c.logger.Error(nil, "failed processing for %s: %v", resultFile.OriginalPath, err)
		}
	}

	return nil
}

func (c *Consumer) Process(file File) (File, error) {
	c.logger.Info(nil, "starting processing for file %s", file.OriginalPath)

	duplicated, err := c.isDuplicate(file.OriginalPath)
	if err != nil {
		return file, fmt.Errorf("failed to check for duplicate: %v", err)
	}

	if duplicated {
		return file, fmt.Errorf("file is a duplicate, skipping")
	}

	file, err = c.extractText(file)
	if err != nil {
		return file, err
	}

	// llm/semantic processing for tags, title, author and doc type

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	datePath := filepath.Join(
		strconv.Itoa(file.Date.Year()),
		fmt.Sprintf("%02d", file.Date.Month()),
		strconv.Itoa(file.Date.Day()),
		// document type name
	)

	storePath := filepath.Join(
		c.config.Storage.StorageDir,
		datePath,
	)
	file.StorageProcessedPath = &storePath

	storeOriginalPath := filepath.Join(
		c.config.Storage.StorageDir,
		"originals",
		datePath,
	)
	file.StorageOriginalPath = &storeOriginalPath

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return file, fmt.Errorf("failed to begin database transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	queries := database.NewQueries(c.db).WithTx(tx)
	result, err := queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title:          file.Name,
		Md5Checksum:    file.MD5Checksum,
		Sha512Checksum: file.SHA512Checksum,
		MimeType:       file.MimeType,
		FileSize:       file.FileSize,
		OriginalPath:   "",
		StoragePath:    *file.StorageProcessedPath,
	})
	if err != nil {
		return file, fmt.Errorf("failed to create document record: %w", err)
	}

	documentID, err := result.LastInsertId()
	if err != nil {
		return file, fmt.Errorf("failed to get document ID: %w", err)
	}

	c.logger.Debug(nil, "Created document with ID: %d", documentID)

	originalFileName := strconv.FormatInt(documentID, 10) + ".pdf"

	fullOriginalPath := filepath.Join(
		*file.StorageOriginalPath,
		originalFileName,
	)
	file.StorageOriginalPath = &fullOriginalPath

	fullStoragePath := filepath.Join(
		*file.StorageProcessedPath,
		originalFileName,
	)
	file.StorageProcessedPath = &fullStoragePath

	c.logger.Debug(nil, "Original path: %s", *file.StorageOriginalPath)
	c.logger.Debug(nil, "Processed path: %s", *file.StorageProcessedPath)

	err = queries.UpdateDocumentPaths(ctx, database.UpdateDocumentPathsParams{
		OriginalPath: *file.StorageOriginalPath,
		StoragePath:  *file.StorageProcessedPath,
		ID:           documentID,
	})
	if err != nil {
		return file, fmt.Errorf("failed to update storage path: %w", err)
	}

	if file.OCRTmpPath != nil {
		c.logger.Debug(
			nil,
			"Moving OCR file from %s to %s",
			*file.OCRTmpPath,
			*file.StorageProcessedPath,
		)
		if err := MoveFile(*file.OCRTmpPath, *file.StorageProcessedPath); err != nil {
			return file, fmt.Errorf("failed to move OCR processed file: %w", err)
		}
		c.logger.Debug(
			nil,
			"Moving original file from %s to %s",
			file.OriginalPath,
			*file.StorageOriginalPath,
		)
		if err := CopyFile(file.OriginalPath, *file.StorageOriginalPath); err != nil {
			c.logger.Error(nil, "Failed to move original file, cleaning up OCR file")
			if removeErr := RemoveFile(*file.StorageProcessedPath); removeErr != nil {
				c.logger.Error(nil, "failed to clean up OCR file: %v", removeErr)
			}
			return file, fmt.Errorf("failed to move original file: %w", err)
		}
	} else {
		c.logger.Debug(nil, "Moving original file from %s to %s", file.OriginalPath, *file.StorageOriginalPath)
		if err := CopyFile(file.OriginalPath, *file.StorageOriginalPath); err != nil {
			return file, fmt.Errorf("failed to move original file: %w", err)
		}
		c.logger.Debug(nil, "Copying file from %s to %s", *file.StorageOriginalPath, *file.StorageProcessedPath)
		if err := CopyFile(*file.StorageOriginalPath, *file.StorageProcessedPath); err != nil {
			c.logger.Error(nil, "Failed to copy file, rolling back original move")
			if rollbackErr := MoveFile(*file.StorageOriginalPath, file.OriginalPath); rollbackErr != nil {
				c.logger.Error(nil, "failed to rollback original file move: %v", rollbackErr)
			}
			return file, fmt.Errorf("failed to copy file to processed storage: %w", err)
		}
	}

	c.logger.Debug(nil, "Committing transaction")
	if err := tx.Commit(); err != nil {
		c.logger.Error(nil, "Transaction commit failed: %v", err)
		c.logger.Error(nil, "Attempting to rollback file operations")
		if removeErr := RemoveFile(*file.StorageProcessedPath); removeErr != nil {
			c.logger.Error(
				nil,
				"Failed to rollback original file move after commit failure: %v",
				removeErr,
			)
		}
		return file, fmt.Errorf("failed to commit transaction: %w", err)
	} else {
		if c.config.Consumer.DeleteOriginal {
			if removeErr := RemoveFile(file.OriginalPath); removeErr != nil {
				c.logger.Error(
					nil,
					"Failed to rollback original file move after commit failure: %v",
					removeErr,
				)
			}
		}
	}

	return file, nil
}

func (c *Consumer) isDuplicate(path string) (bool, error) {
	md5sum, err := calculateMD5(path)
	if err != nil {
		return false, err
	}

	c.logger.Debug(nil, "MD5: %s", md5sum)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	queries := database.NewQueries(c.db)

	md5Result, err := queries.GetDocumentByMD5Checksum(ctx, md5sum)
	if err != nil {
		return false, err
	}

	c.logger.Debug(nil, "MD5 matches: %d", len(md5Result))

	if len(md5Result) == 0 {
		return false, nil
	}

	sha512sum, err := calculateSHA512(path)
	if err != nil {
		return false, err
	}

	c.logger.Debug(nil, "SHA512: %s", sha512sum)

	_, err = queries.GetDocumentBySHA512Checksum(ctx, sha512sum)
	if err != nil {
		c.logger.Debug(nil, "SHA512 check error: %v", err)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func calculateMD5(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := md5.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to calculate MD5: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func calculateSHA512(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hasher := sha512.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to calculate SHA512: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (c *Consumer) extractText(file File) (File, error) {
	runner := tools.NewRunner(c.logger, &c.config.Consumer)

	extractResult, err := runner.ExtractText(file.OriginalPath)
	if err != nil {
		return file, fmt.Errorf("text extraction failed: %w", err)
	}

	if extractResult.Text != nil && *extractResult.Text != "" {
		file.Text = extractResult.Text
		return file, nil
	}

	c.logger.Info(nil, "no text extracted from %s, OCR needed", file.Name)

	ocrResult, err := runner.OCR(file.OriginalPath)
	if err != nil {
		return file, err
	}

	extractResult, err = runner.ExtractText(*ocrResult.TmpPath)
	if err != nil {
		return file, fmt.Errorf("text extraction failed for ocrd file: %w", err)
	}

	file.Text = extractResult.Text
	file.OCRTmpPath = ocrResult.TmpPath

	return file, nil
}
