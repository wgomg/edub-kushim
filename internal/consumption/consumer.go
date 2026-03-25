package consumption

import (
	"context"
	"database/sql"
	"fmt"
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
		storeOriginalPath := filepath.Join(c.config.Storage.StorageDir, "originals", file.Name)
		file.StorageOriginalPath = &storeOriginalPath

		c.Process(file)
	}

	return nil
}

func (c *Consumer) Process(file File) (File, error) {
	file, err := c.extractText(file)
	if err != nil {
		return file, err
	}

	// llm/semantic processing for tags, title, author and doc type

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	storePath := filepath.Join(
		c.config.Storage.StorageDir,
		strconv.Itoa(file.Date.Year()),
		fmt.Sprintf("%02d", file.Date.Month()),
		strconv.Itoa(file.Date.Day()),
		// document type name
	)
	file.StorageProcessedPath = &storePath

	queries := database.NewQueries(c.db)
	result, err := queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title:          file.Name,
		Md5Checksum:    file.MD5Checksum,
		Sha512Checksum: file.SHA512Checksum,
		MimeType:       file.MimeType,
		FileSize:       file.FileSize,
		OriginalPath:   *file.StorageOriginalPath,
		StoragePath:    *file.StorageProcessedPath,
	})
	if err != nil {
		return file, fmt.Errorf("failed to create document record: %w", err)
	}

	documentID, err := result.LastInsertId()
	if err != nil {
		return file, fmt.Errorf("failed to get document ID: %w", err)
	}

	fullStoragePath := filepath.Join(
		*file.StorageOriginalPath,
		strconv.FormatInt(documentID, 10)+".pdf",
	)

	err = queries.UpdateDocument(ctx, database.UpdateDocumentParams{
		StoragePath: fullStoragePath,
		ID:          documentID,
	})
	if err != nil {
		return file, fmt.Errorf("failed to update storage path: %w", err)
	}

	file.StorageProcessedPath = &fullStoragePath

	if err := MoveFile(file.OriginalPath, *file.StorageOriginalPath); err != nil {
		// TODO: rollback database transaction
		return file, fmt.Errorf("failed to move original file: %w", err)
	}

	srcProcessedPath := file.OriginalPath
	if file.OCRTmpPath != nil {
		srcProcessedPath = *file.OCRTmpPath
	}
	if err := MoveFile(srcProcessedPath, *file.StorageProcessedPath); err != nil {
		// TODO: rollback database transaction
		return file, fmt.Errorf("failed to move processed file: %w", err)
	}

	return file, nil
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
