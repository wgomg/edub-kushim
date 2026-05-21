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
	"github.com/wgomg/edub-kushim/internal/tools/adapters/pdfoptimizer"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type Consumer struct {
	config *config.Config
	logger *utils.Logger
	db     *sql.DB
	runner *tools.Runner
}

type File struct {
	Name                 string
	OriginalPath         string
	OCRTmpPath           *string
	OptimizedPdfTmpPath  *string
	StorageProcessedPath *string
	StorageOriginalPath  *string
	DocumentID           sql.NullInt64
	MD5Checksum          string
	SHA512Checksum       string
	Text                 sql.NullString
	MimeType             string
	Date                 time.Time
	FileSize             int64
}

func NewConsumer(cfg *config.Config, logger *utils.Logger, db *sql.DB) (*Consumer, error) {
	if cfg.Consumer.OptimizationFallback != "" {
		if _, err := pdfoptimizer.NewPdfOptimizer(logger, config.ToolConfig{
			Command: cfg.Consumer.OptimizationFallback,
			Timeout: 30 * time.Second,
		}); err != nil {
			return nil, fmt.Errorf(
				"optimization_fallback %q not available: %w — "+
					"install it or set optimization_fallback to \"\" (empty) to disable",
				cfg.Consumer.OptimizationFallback, err)
		}
	}
	return &Consumer{
		config: cfg,
		logger: logger,
		db:     db,
		runner: tools.NewRunner(logger, &cfg.Consumer),
	}, nil
}

func NewConsumerWithRunner(cfg *config.Config, logger *utils.Logger, db *sql.DB, runner *tools.Runner) (*Consumer, error) {
	return &Consumer{
		config: cfg,
		logger: logger,
		db:     db,
		runner: runner,
	}, nil
}

func (c *Consumer) Process(file File) (File, error) {
	start := time.Now()
	c.logger.Info(nil, "starting processing for file %s", file.OriginalPath)

	defer func() {
		elapsed := time.Since(start)
		if file.StorageProcessedPath != nil {
			c.logger.Info(nil, "finished processing %s in %s -> %s", file.OriginalPath, humanDuration(elapsed), *file.StorageProcessedPath)
		} else {
			c.logger.Info(nil, "finished processing %s in %s (skipped)", file.OriginalPath, humanDuration(elapsed))
		}
	}()

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
		fmt.Sprintf("%02d", file.Date.Day()),
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

		if file.OCRTmpPath != nil {
			if err := CleanUp(*file.OCRTmpPath); err != nil {
				c.logger.Debug(nil, "failed to clean up ocr temp file: %v", err)
			}
		}

		if file.OptimizedPdfTmpPath != nil {
			if err := CleanUp(*file.OptimizedPdfTmpPath); err != nil {
				c.logger.Debug(nil, "failed to clean up optimized temp file: %v", err)
			}
		}
	}()

	// timestamp := time.Now().UnixNano()
	// uniqueSeed := fmt.Sprintf("%s:%d", file.OriginalPath, timestamp)

	// md5Hash := fmt.Sprintf("%x", md5.Sum([]byte(uniqueSeed)))
	// sha512Hash := fmt.Sprintf("%x", sha512.Sum512([]byte(uniqueSeed)))

	// file.MD5Checksum = md5Hash
	// file.SHA512Checksum = sha512Hash

	queries := database.NewQueries(c.db).WithTx(tx)
	result, err := queries.CreateDocument(ctx, database.CreateDocumentParams{
		Title:          file.Name,
		Md5Checksum:    file.MD5Checksum,
		Sha512Checksum: file.SHA512Checksum,
		MimeType:       file.MimeType,
		FileSize:       file.FileSize,
		OriginalPath:   "",
		StoragePath:    *file.StorageProcessedPath,
		TextContent:    file.Text,
	})
	if err != nil {
		return file, fmt.Errorf("failed to create document record: %w", err)
	}

	documentID, err := result.LastInsertId()
	if err != nil {
		return file, fmt.Errorf("failed to get document ID: %w", err)
	}

	c.logger.Debug(nil, "Created document with ID: %d", documentID)

	file.DocumentID = sql.NullInt64{Int64: documentID, Valid: true}

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
	} else if file.OptimizedPdfTmpPath != nil {
		c.logger.Debug(nil,
			"Copying original file from %s to %s", file.OriginalPath, *file.StorageOriginalPath)
		if err := CopyFile(file.OriginalPath, *file.StorageOriginalPath); err != nil {
			return file, fmt.Errorf("failed to copy original file: %w", err)
		}

		c.logger.Debug(nil,
			"Moving optimized file from %s to %s", *file.OptimizedPdfTmpPath, *file.StorageProcessedPath)
		if err := MoveFile(*file.OptimizedPdfTmpPath, *file.StorageProcessedPath); err != nil {
			return file, fmt.Errorf("failed to move optimized file: %w", err)
		}
	} else {
		// optimization failed or was skipped — use original for both.
		c.logger.Debug(nil,
			"Copying original file from %s to %s (no optimized version)", file.OriginalPath, *file.StorageProcessedPath)
		if err := CopyFile(file.OriginalPath, *file.StorageProcessedPath); err != nil {
			return file, fmt.Errorf("failed to copy original file to processed storage: %w", err)
		}
		c.logger.Debug(nil,
			"Copying original file from %s to %s", file.OriginalPath, *file.StorageOriginalPath)
		if err := CopyFile(file.OriginalPath, *file.StorageOriginalPath); err != nil {
			return file, fmt.Errorf("failed to copy original file to originals storage: %w", err)
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	queries := database.NewQueries(c.db)

	md5Result, err := queries.GetDocumentByMD5Checksum(ctx, md5sum)
	if err != nil {
		return false, err
	}

	if len(md5Result) == 0 {
		return false, nil
	}

	sha512sum, err := calculateSHA512(path)
	if err != nil {
		return false, err
	}

	_, err = queries.GetDocumentBySHA512Checksum(ctx, sha512sum)
	if err != nil {
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

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		d = d.Round(time.Second)
		m := int(d.Minutes())
		s := int(d.Seconds()) - m*60
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		d = d.Round(time.Second)
		h := int(d.Hours())
		d -= time.Duration(h) * time.Hour
		m := int(d.Minutes())
		s := int(d.Seconds()) - m*60
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
}

func (c *Consumer) extractText(file File) (File, error) {
	ctx := context.Background()

	memBefore := utils.ReadMemSnapshot()
	extractResult, err := c.runner.ExtractText(ctx, file.OriginalPath)
	memAfterExtract := utils.ReadMemSnapshot()
	c.logger.Debug(nil, "extractText: %s", utils.FormatMemDelta(memBefore, memAfterExtract))
	if err != nil {
		return file, fmt.Errorf("text extraction failed: %w", err)
	}

	const minTextDensityRatio = 0.001
	if extractResult.Text != nil && *extractResult.Text != "" && float64(len(*extractResult.Text))/float64(file.FileSize) >= minTextDensityRatio {
		file.Text = sql.NullString{String: *extractResult.Text, Valid: true}

		optimizationResult, err := c.runner.OptimizePdf(ctx, file.OriginalPath)
		memAfterOpt := utils.ReadMemSnapshot()
		c.logger.Debug(nil, "optimizePdf: %s", utils.FormatMemDelta(memAfterExtract, memAfterOpt))
		if err != nil {
			c.logger.Info(nil, "optimization failed for %s, using original: %v", file.Name, err)
		} else {
			file.OptimizedPdfTmpPath = optimizationResult.TmpPath
		}

		return file, nil
	}

	c.logger.Info(nil, "no text extracted from %s, OCR needed", file.Name)

	ocrResult, err := c.runner.OCR(ctx, file.OriginalPath)
	memAfterOCR := utils.ReadMemSnapshot()
	c.logger.Debug(nil, "OCR: %s", utils.FormatMemDelta(memAfterExtract, memAfterOCR))
	if err != nil {
		return file, err
	}

	extractResult, err = c.runner.ExtractText(ctx, *ocrResult.TmpPath)
	memAfterFinal := utils.ReadMemSnapshot()
	c.logger.Debug(nil, "extractText (post-OCR): %s", utils.FormatMemDelta(memAfterOCR, memAfterFinal))
	if err != nil {
		return file, fmt.Errorf("text extraction failed for ocrd file: %w", err)
	}

	if extractResult.Text != nil && *extractResult.Text != "" {
		file.Text = sql.NullString{String: *extractResult.Text, Valid: true}
	} else {
		file.Text = sql.NullString{Valid: false}
	}
	file.OCRTmpPath = ocrResult.TmpPath

	return file, nil
}
