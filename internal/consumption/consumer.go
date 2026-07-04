package consumption

import (
	"context"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/tools/adapters"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/pdfoptimizer"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	errorDirName     = "errors"
	errorDirNameDupes = "duplicated"
)

// runner is an interface covering the subset of *tools.Runner methods that
// Consumer.Process depends on. *tools.Runner satisfies it automatically.
// This extraction exists solely to allow mock implementations in tests.
type runner interface {
	ExtractText(ctx context.Context, path string) (*tools.TextExtractionResult, error)
	OCR(ctx context.Context, docId, path string) (*tools.OCRResult, error)
	OptimizePdf(ctx context.Context, docId, path string) (*tools.PdfOptimizationResult, error)
}

var _ runner = (*tools.Runner)(nil)

type Consumer struct {
	config *config.Config
	logger *utils.Logger
	client *database.Client
	runner runner
}

type File struct {
	Name                 string
	OriginalPath         string
	OCRTmpPath           *string
	OptimizedPdfTmpPath  *string
	StorageProcessedPath *string
	StorageOriginalPath  *string
	DocumentID           string
	DocumentDbId         sql.NullInt64
	MD5Checksum          string
	SHA512Checksum       string
	Text                 sql.NullString
	MimeType             string
	Date                 time.Time
	FileSize             int64
	PageCount            int
}

func NewConsumer(cfg *config.Config, logger *utils.Logger, client *database.Client) (*Consumer, error) {
	if cfg.Consumer.PdfOptimizer.Fallback != "" {
		if _, err := pdfoptimizer.NewPdfOptimizer(logger, config.ToolConfig{
			Command: cfg.Consumer.PdfOptimizer.Fallback,
			Timeout: 30 * time.Second,
		}); err != nil {
			return nil, fmt.Errorf(
				"pdfoptimizer.fallback %q not available: %w — "+
					"install it or set pdfoptimizer.fallback to \"\" (empty) to disable",
				cfg.Consumer.PdfOptimizer.Fallback, err)
		}
	}
	return &Consumer{
		config: cfg,
		logger: logger,
		client: client,
		runner: tools.NewRunner(logger, cfg, []string{"textextractor", "ocr", "pdfoptimizer"}),
	}, nil
}

func NewConsumerWithRunner(cfg *config.Config, logger *utils.Logger, client *database.Client, runner runner) (*Consumer, error) {
	return &Consumer{
		config: cfg,
		logger: logger,
		client: client,
		runner: runner,
	}, nil
}

func (c *Consumer) Process(ctx context.Context, file File, documentID string) (File, error) {
	start := time.Now()
	c.logger.Info(&documentID, "starting consumption for file %s", file.OriginalPath)

	defer func() {
		elapsed := time.Since(start)
		if file.StorageProcessedPath != nil {
			c.logger.Info(&documentID, "finished consumption %s in %s -> %s", file.OriginalPath, utils.HumanDuration(elapsed), *file.StorageProcessedPath)
		} else {
			c.logger.Info(&documentID, "finished consumption %s in %s (skipped)", file.OriginalPath, utils.HumanDuration(elapsed))
		}
	}()

	duplicated, err := c.isDuplicate(ctx, file.OriginalPath)
	if err != nil {
		MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
		return file, fmt.Errorf("failed to check for duplicate: %v", err)
	}

	if duplicated {
		MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "duplicate", c.logger, &documentID)
		return file, fmt.Errorf("file is a duplicate, skipping")
	}

	file, err = c.extractText(ctx, file, documentID)
	if err != nil {
		MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
		return file, err
	}

	txCtx, txCancel := context.WithTimeout(ctx, 5*time.Second)
	defer txCancel()

	datePath := filepath.Join(
		strconv.Itoa(file.Date.Year()),
		fmt.Sprintf("%02d", file.Date.Month()),
		fmt.Sprintf("%02d", file.Date.Day()),
		fmt.Sprintf("%02d", file.Date.Hour()),
	)

	storePath := filepath.Join(
		c.config.Storage.StorageDir,
		"processed",
		datePath,
	)
	file.StorageProcessedPath = &storePath

	storeOriginalPath := filepath.Join(
		c.config.Storage.StorageDir,
		"originals",
		datePath,
	)
	file.StorageOriginalPath = &storeOriginalPath

	tx, err := 	c.client.BeginTx(txCtx, nil)
	if err != nil {
		MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
		return file, fmt.Errorf("failed to begin database transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
			if file.StorageProcessedPath != nil && strings.HasSuffix(*file.StorageProcessedPath, ".pdf") {
				if removeErr := RemoveFile(*file.StorageProcessedPath); removeErr != nil {
					c.logger.Error(&documentID, "failed to clean up processed storage file: %v", removeErr)
				}
			}
			if file.StorageOriginalPath != nil && strings.HasSuffix(*file.StorageOriginalPath, ".pdf") {
				if removeErr := RemoveFile(*file.StorageOriginalPath); removeErr != nil {
					c.logger.Error(&documentID, "failed to clean up original storage file: %v", removeErr)
				}
			}
		}

		if file.OCRTmpPath != nil {
			if err := CleanUp(*file.OCRTmpPath); err != nil {
				c.logger.Debug(&documentID, "failed to clean up ocr temp file: %v", err)
			}
		}

		if file.OptimizedPdfTmpPath != nil {
			if err := CleanUp(*file.OptimizedPdfTmpPath); err != nil {
				c.logger.Debug(&documentID, "failed to clean up optimized temp file: %v", err)
			}
		}
	}()

	// timestamp := time.Now().UnixNano()
	// uniqueSeed := fmt.Sprintf("%s:%d", file.OriginalPath, timestamp)

	// md5Hash := fmt.Sprintf("%x", md5.Sum([]byte(uniqueSeed)))
	// sha512Hash := fmt.Sprintf("%x", sha512.Sum512([]byte(uniqueSeed)))

	// file.MD5Checksum = md5Hash
	// file.SHA512Checksum = sha512Hash

	tq := c.client.WithTx(tx)
	result, err := tq.CreateDocument(txCtx, database.CreateDocumentParams{
		DocumentID:     documentID,
		Title:          file.Name,
		Md5Checksum:    file.MD5Checksum,
		Sha512Checksum: file.SHA512Checksum,
		MimeType:       file.MimeType,
		FileSize:       file.FileSize,
		OriginalPath:   "",
		StoragePath:    *file.StorageProcessedPath,
		TextContent:    file.Text,
		PageCount:      int64(file.PageCount),
		WordCount:      int64(len(strings.Fields(file.Text.String))),
		CharCount:      int64(utf8.RuneCountInString(file.Text.String)),
	})
	if err != nil {
		MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
		return file, fmt.Errorf("failed to create document record: %w", err)
	}

	documentDbId, err := result.LastInsertId()
	if err != nil {
		MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
		return file, fmt.Errorf("failed to get document ID: %w", err)
	}

	c.logger.Debug(&documentID, "Created document with ID: %d", documentDbId)

	file.DocumentID = documentID
	file.DocumentDbId = sql.NullInt64{Int64: documentDbId, Valid: true}

	originalFileName := documentID + ".pdf"

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

	c.logger.Debug(&documentID, "Original path: %s", *file.StorageOriginalPath)
	c.logger.Debug(&documentID, "Processed path: %s", *file.StorageProcessedPath)

	err = tq.UpdateDocumentPaths(txCtx, database.UpdateDocumentPathsParams{
		OriginalPath: *file.StorageOriginalPath,
		StoragePath:  *file.StorageProcessedPath,
		DocumentID:   documentID,
	})
	if err != nil {
		MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
		return file, fmt.Errorf("failed to update storage path: %w", err)
	}

	if file.OCRTmpPath != nil {
		c.logger.Debug(
			&documentID,
			"Moving OCR file from %s to %s",
			*file.OCRTmpPath,
			*file.StorageProcessedPath,
		)
		if err := MoveFile(*file.OCRTmpPath, *file.StorageProcessedPath); err != nil {
			MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
			return file, fmt.Errorf("failed to move OCR processed file: %w", err)
		}
		c.logger.Debug(
			&documentID,
			"Moving original file from %s to %s",
			file.OriginalPath,
			*file.StorageOriginalPath,
		)
		if err := CopyFile(file.OriginalPath, *file.StorageOriginalPath); err != nil {
			c.logger.Error(&documentID, "Failed to move original file, cleaning up OCR file")
			if removeErr := RemoveFile(*file.StorageProcessedPath); removeErr != nil {
				c.logger.Error(&documentID, "failed to clean up OCR file: %v", removeErr)
			}
			MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
			return file, fmt.Errorf("failed to move original file: %w", err)
		}
	} else if file.OptimizedPdfTmpPath != nil {
		c.logger.Debug(&documentID,
			"Copying original file from %s to %s", file.OriginalPath, *file.StorageOriginalPath)
		if err := CopyFile(file.OriginalPath, *file.StorageOriginalPath); err != nil {
			MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
			return file, fmt.Errorf("failed to copy original file: %w", err)
		}

		c.logger.Debug(&documentID,
			"Moving optimized file from %s to %s", *file.OptimizedPdfTmpPath, *file.StorageProcessedPath)
		if err := MoveFile(*file.OptimizedPdfTmpPath, *file.StorageProcessedPath); err != nil {
			MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
			return file, fmt.Errorf("failed to move optimized file: %w", err)
		}
	} else {
		// optimization failed or was skipped — use original for both.
		c.logger.Debug(&documentID,
			"Copying original file from %s to %s (no optimized version)", file.OriginalPath, *file.StorageProcessedPath)
		if err := CopyFile(file.OriginalPath, *file.StorageProcessedPath); err != nil {
			MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
			return file, fmt.Errorf("failed to copy original file to processed storage: %w", err)
		}
		c.logger.Debug(&documentID,
			"Copying original file from %s to %s", file.OriginalPath, *file.StorageOriginalPath)
		if err := CopyFile(file.OriginalPath, *file.StorageOriginalPath); err != nil {
			MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
			return file, fmt.Errorf("failed to copy original file to originals storage: %w", err)
		}
	}

	c.logger.Debug(&documentID, "Committing transaction")
	if err := tx.Commit(); err != nil {
		c.logger.Error(&documentID, "Transaction commit failed: %v", err)
		c.logger.Error(&documentID, "Attempting to rollback file operations")
		if removeErr := RemoveFile(*file.StorageProcessedPath); removeErr != nil {
			c.logger.Error(
				&documentID,
				"Failed to rollback original file move after commit failure: %v",
				removeErr,
			)
		}
		MoveFailedFile(c.config.Storage.StorageDir, file.OriginalPath, "", c.logger, &documentID)
		return file, fmt.Errorf("failed to commit transaction: %w", err)
	} else {
		if removeErr := RemoveFile(file.OriginalPath); removeErr != nil {
			c.logger.Error(
				&documentID,
				"Failed to remove original file from inbox after successful processing: %v",
				removeErr,
			)
		}
	}

	return file, nil
}

func MoveFailedFile(storageDir, originalPath, errType string, logger *utils.Logger, docID *string) {
	baseName := filepath.Base(originalPath)
	uuidStr := uuid.New().String()
	destName := uuidStr + "-" + baseName

	var destDir string
	if errType == "duplicate" {
		destDir = filepath.Join(storageDir, errorDirName, errorDirNameDupes)
	} else {
		destDir = filepath.Join(storageDir, errorDirName)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		logger.Error(docID, "Failed to create error directory %s: %v", destDir, err)
		return
	}

	destPath := filepath.Join(destDir, destName)
	if err := MoveFile(originalPath, destPath); err != nil {
		logger.Error(docID, "Failed to move failed file %s to %s: %v", originalPath, destPath, err)
		return
	}

	logger.Info(docID, "Moved failed file to %s", destPath)
}

type consumePayload struct {
	FilePath    string `json:"file_path"`
	OnCompleted string `json:"on_completed"`
}

func QuarantineFailedFiles(ctx context.Context, queries *database.Queries, storageDir string, logger *utils.Logger, batchID string) error {
	rows, err := queries.GetQuarantinedConsumeTaskPayloads(ctx, sql.NullString{String: batchID, Valid: true})
	if err != nil {
		return fmt.Errorf("get quarantined consume task payloads: %w", err)
	}

	var firstErr error
	for _, row := range rows {
		var p consumePayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("unmarshal payload for task %s: %w", row.TaskID, err)
			}
			continue
		}

		if p.FilePath != "" {
			MoveFailedFile(storageDir, p.FilePath, "", logger, nil)
		}

		if p.OnCompleted != "" {
			if _, err := queries.DiscardEnrichTaskByTaskID(ctx, database.DiscardEnrichTaskByTaskIDParams{
				TaskID: p.OnCompleted,
				Error:  sql.NullString{String: "parent task quarantined", Valid: true},
			}); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("discard enrich task %s: %w", p.OnCompleted, err)
				}
			}
		}
	}
	return firstErr
}

func (c *Consumer) isDuplicate(ctx context.Context, path string) (bool, error) {
	md5sum, err := utils.CalculateMD5(path)
	if err != nil {
		return false, err
	}

	dupCtx, dupCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dupCancel()

	md5Result, err := c.client.GetDocumentByMD5Checksum(dupCtx, md5sum)
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

	_, err = c.client.GetDocumentBySHA512Checksum(dupCtx, sha512sum)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	return true, nil
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

func (c *Consumer) extractText(ctx context.Context, file File, documentID string) (File, error) {
	memBefore := utils.ReadMemSnapshot()

	if file.MimeType == "application/pdf" {
		file.PageCount = countPages(file.OriginalPath)
	}

	extractResult, err := c.runner.ExtractText(ctx, file.OriginalPath)
	memAfterExtract := utils.ReadMemSnapshot()
	c.logger.Debug(&documentID, "extractText: %s", utils.FormatMemDelta(memBefore, memAfterExtract))
	if err != nil {
		return file, fmt.Errorf("text extraction failed: %w", err)
	}

	const minTextDensityRatio = 0.001
	if extractResult.Text != nil && *extractResult.Text != "" && float64(len(*extractResult.Text))/float64(file.FileSize) >= minTextDensityRatio {
		file.Text = sql.NullString{String: *extractResult.Text, Valid: true}

		c.logger.Debug(&documentID, "optimizePdf: entering for %s", file.OriginalPath)
		optimizationResult, err := c.runner.OptimizePdf(ctx, documentID, file.OriginalPath)
		memAfterOpt := utils.ReadMemSnapshot()
		c.logger.Debug(&documentID, "optimizePdf: %s", utils.FormatMemDelta(memAfterExtract, memAfterOpt))
		if err != nil {
			c.logger.Info(&documentID, "optimization failed for %s, using original: %v", file.Name, err)
		} else {
			file.OptimizedPdfTmpPath = optimizationResult.TmpPath
		}

		return file, nil
	}

	c.logger.Info(&documentID, "no text extracted from %s, OCR needed", file.Name)

	ocrResult, err := c.runner.OCR(ctx, documentID, file.OriginalPath)
	memAfterOCR := utils.ReadMemSnapshot()
	c.logger.Debug(&documentID, "OCR: %s (RSS now %s)", utils.FormatMemDelta(memAfterExtract, memAfterOCR), utils.FormatBytes(memAfterOCR.RSS))
	if err != nil {
		c.logger.Error(&documentID, "OCR failed for %s: %v", file.Name, err)
		return file, err
	}

	extractResult, err = c.runner.ExtractText(ctx, *ocrResult.TmpPath)
	memAfterFinal := utils.ReadMemSnapshot()
	c.logger.Debug(&documentID, "extractText (post-OCR): %s", utils.FormatMemDelta(memAfterOCR, memAfterFinal))
	if err != nil {
		return file, fmt.Errorf("text extraction failed for ocrd file: %w", err)
	}

	if extractResult.Text != nil && *extractResult.Text != "" {
		file.Text = sql.NullString{String: *extractResult.Text, Valid: true}
	} else {
		file.Text = sql.NullString{Valid: false}
	}
	file.OCRTmpPath = ocrResult.TmpPath

	c.logger.Debug(&documentID, "extractText complete: RSS %s (total delta: %s)",
		utils.FormatBytes(memAfterFinal.RSS),
		utils.FormatMemDelta(memBefore, memAfterFinal))

	return file, nil
}

func countPages(path string) int {
	mupdfCtx, err := adapters.NewMuContext()
	if err != nil {
		return 0
	}
	defer mupdfCtx.Close()

	doc, err := mupdfCtx.OpenMuDocument(path)
	if err != nil {
		return 0
	}
	defer doc.Close(mupdfCtx)

	return doc.NumPages(mupdfCtx)
}
