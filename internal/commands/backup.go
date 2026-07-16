package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/database"
)

func backupHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)
	if fp.Help("Usage: kushim backup [--path <dir>]\n"+
		"  Run a backup immediately.\n\n"+
		"  --path    Override output directory (default: config backup.path)") {
		return nil
	}

	var overridePath string
	fp.String("--path", &overridePath)
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown arguments: %v", rest)
	}

	if !c.backupMu.TryLock() {
		return fmt.Errorf("backup already in progress")
	}
	defer c.backupMu.Unlock()

	backupDir := c.config.Backup.Path
	if overridePath != "" {
		backupDir = overridePath
	}

	db, err := c.GetDB()
	if err != nil {
		return err
	}
	configPath := filepath.Join(c.config.App.ConfigDir, "config.yaml")

	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	fmt.Println("Creating backup...")
	result, err := backup.Create(ctx, db, database.SchemaFS, backupDir, configPath, c.config.Storage.StorageDir)
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	fmt.Printf("Backup created: %s\n", result.Path)
	fmt.Printf("  Archive size: %d bytes\n", result.SizeBytes)
	fmt.Printf("  Database size: %d bytes\n", result.DbSizeBytes)
	fmt.Printf("  Storage files: %d\n", result.FilesCount)
	if result.Manifest != nil {
		fmt.Printf("  Backup time: %s\n", result.Manifest.Timestamp)
	}

	if overridePath == "" && c.config.Backup.Keep > 0 {
		if err := backup.ApplyRetention(c.config.Backup.Path, c.config.Backup.Keep); err != nil {
			fmt.Printf("Warning: retention cleanup failed: %v\n", err)
		}
	}

	return nil
}

func restoreHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)
	if fp.Help("Usage: kushim restore <backup-file.tar.gz> [--force] [--dry-run]\n"+
		"  Restore from a backup archive.\n\n"+
		"  --force     Skip confirmation prompt\n"+
		"  --dry-run   Validate backup without restoring") {
		return nil
	}

	force := false
	fp.Bool("--force", &force)
	dryRun := false
	fp.Bool("--dry-run", &dryRun)

	rest := fp.Rest()
	if len(rest) == 0 {
		return fmt.Errorf("missing backup file argument")
	}
	archivePath := rest[0]

	if _, err := os.Stat(archivePath); err != nil {
		return fmt.Errorf("backup file not found: %s", archivePath)
	}

	manifest, err := backup.ValidateArchive(archivePath)
	if err != nil {
		return fmt.Errorf("invalid backup archive: %w", err)
	}

	fmt.Println("Backup manifest:")
	fmt.Printf("  Version: %d\n", manifest.Version)
	fmt.Printf("  Timestamp: %s\n", manifest.Timestamp)
	fmt.Printf("  App version: %s\n", manifest.AppVersion)
	fmt.Printf("  Database size: %d bytes\n", manifest.DbSizeBytes)
	fmt.Printf("  Storage files: %d (%d bytes)\n", manifest.StorageFilesCount, manifest.StorageSizeBytes)
	fmt.Printf("  Config hash: %s\n", manifest.ConfigHash)

	if dryRun {
		fmt.Println("\nDry-run: archive is valid")
		return nil
	}

	if !c.backupMu.TryLock() {
		return fmt.Errorf("another backup or restore is in progress")
	}
	defer c.backupMu.Unlock()

	pidFile := filepath.Join(c.config.App.ConfigDir, "kushim-queue.pid")
	if data, err := os.ReadFile(pidFile); err == nil {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr == nil && syscall.Kill(pid, 0) == nil {
			if !force {
				return fmt.Errorf("queue daemon is running (PID %d) — stop it first or use --force", pid)
			}
			fmt.Printf("Warning: queue daemon is running (PID %d), restoring with --force\n", pid)
		}
	}

	if !force {
		fmt.Println("\nWARNING: Restore will replace the current database, configuration, and storage files.")
		fmt.Print("Are you sure? (type 'yes' to confirm): ")
		var confirmation string
		fmt.Scanln(&confirmation)
		if confirmation != "yes" {
			fmt.Println("Restore cancelled.")
			return nil
		}
	}

	tmpDir, err := os.MkdirTemp("", "edub-restore-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Println("Extracting archive...")
	if err := backup.ExtractArchive(archivePath, tmpDir); err != nil {
		return fmt.Errorf("extract archive: %w", err)
	}

	db, err := c.GetDB()
	if err != nil {
		return err
	}
	configPath := filepath.Join(c.config.App.ConfigDir, "config.yaml")

	fmt.Println("Replacing files...")
	if err := backup.ReplaceFiles(tmpDir, db, configPath, c.config.Storage.StorageDir); err != nil {
		return fmt.Errorf("replace files: %w", err)
	}

	fmt.Println("Restore completed successfully.")
	fmt.Println("Restart the services to pick up the restored data.")

	return nil
}
