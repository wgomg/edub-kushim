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
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func checkBackupPreconditions(c *Container, operation string) (*database.Client, error) {
	client, err := c.GetClient()
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}

	locked, err := client.Queries.IsBackupLocked(context.Background())
	if err != nil {
		return nil, fmt.Errorf("check backup state: %w", err)
	}
	if locked > 0 {
		return nil, fmt.Errorf("a scheduled backup is in progress — wait for it to finish")
	}

	count, err := client.Queries.CountProcessingTasks(context.Background())
	if err != nil {
		return nil, fmt.Errorf("check processing tasks: %w", err)
	}
	if count > 0 {
		return nil, fmt.Errorf("%d task(s) in progress — stop all processing before running manual %s", count, operation)
	}

	if c.cfg.Load().Consumer.Polling.Enabled {
		return nil, fmt.Errorf("polling is enabled — disable it before running manual %s", operation)
	}

	return client, nil
}

func backupHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)
	if fp.Help("Usage: kushim backup [--path <dir>] [--mode <full|database|documents>]\n" +
		"  Run a backup immediately.\n\n" +
		"  --path    Override output directory (default: config backup.path)\n" +
		"  --mode    What to include: full (DB + documents), database, documents (default: full)") {
		return nil
	}

	var overridePath string
	var mode string
	fp.String("--path", &overridePath)
	fp.String("--mode", &mode)
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown arguments: %v", rest)
	}

	backupMode := backup.BackupMode(mode)
	if backupMode == "" {
		backupMode = backup.BackupModeFull
	}
	if !backupMode.Valid() {
		return fmt.Errorf("invalid --mode %q — must be one of: full, database, documents", mode)
	}

	client, err := checkBackupPreconditions(c, "backup")
	if err != nil {
		return err
	}

	rowsAffected, err := client.Queries.AcquireBackupLock(context.Background())
	if err != nil {
		return fmt.Errorf("acquire backup lock: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("backup lock held by another process — wait for it to finish")
	}
	defer client.Queries.ReleaseBackupLock(context.Background())

	backupDir := c.cfg.Load().Backup.Path
	if overridePath != "" {
		backupDir = overridePath
	}

	db, err := c.GetDB()
	if err != nil {
		return err
	}
	configPath := filepath.Join(c.cfg.Load().App.ConfigDir, "config.yaml")

	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	fmt.Printf("Creating %s backup...\n", backupMode)
	result, err := backup.Create(ctx, db, database.SchemaFS, backupMode, backupDir, configPath, c.cfg.Load().Storage.StorageDir)
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	fmt.Printf("Backup created: %s\n", result.Path)
	fmt.Printf("  Mode: %s\n", backupMode)
	fmt.Printf("  Archive size: %d bytes\n", result.SizeBytes)
	fmt.Printf("  Database size: %d bytes\n", result.DbSizeBytes)
	fmt.Printf("  Storage files: %d\n", result.FilesCount)
	if result.Manifest != nil {
		fmt.Printf("  Backup time: %s\n", result.Manifest.Timestamp)
	}

	return nil
}

func restoreHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)
	if fp.Help("Usage: kushim restore <backup-file.tar.gz> [--force] [--dry-run] [--temp-dir <dir>]\n" +
		"  Restore from a backup archive.\n\n" +
		"  --force     Skip confirmation prompt\n" +
		"  --dry-run   Validate backup without restoring\n" +
		"  --temp-dir <dir>  Directory for extraction staging (default: next to storage_dir)") {
		return nil
	}

	force := false
	fp.Bool("--force", &force)
	dryRun := false
	fp.Bool("--dry-run", &dryRun)
	var tempDirOverride string
	fp.String("--temp-dir", &tempDirOverride)

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
	mode := manifest.Mode
	if mode == "" {
		mode = backup.BackupModeFull
	}
	fmt.Printf("  Mode: %s\n", mode)
	fmt.Printf("  Timestamp: %s\n", manifest.Timestamp)
	fmt.Printf("  App version: %s\n", manifest.AppVersion)
	fmt.Printf("  Database size: %d bytes\n", manifest.DbSizeBytes)
	fmt.Printf("  Storage files: %d (%d bytes)\n", manifest.StorageFilesCount, manifest.StorageSizeBytes)
	fmt.Printf("  Config hash: %s\n", manifest.ConfigHash)

	if dryRun {
		fmt.Println("\nDry-run: archive is valid")
		return nil
	}

	if mode != backup.BackupModeDocuments {
		cfg := c.cfg.Load()
		if err := database.CheckRestoreTooling(cfg.Db.Runtime, cfg.Db.Container); err != nil {
			return err
		}
	}

	client, err := checkBackupPreconditions(c, "restore")
	if err != nil {
		return err
	}

	rowsAffected, err := client.Queries.AcquireBackupLock(context.Background())
	if err != nil {
		return fmt.Errorf("acquire backup lock: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("backup lock held by another process — wait for it to finish")
	}
	defer client.Queries.ReleaseBackupLock(context.Background())

	pidFile := filepath.Join(c.cfg.Load().App.ConfigDir, "kushim-queue.pid")
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
		fmt.Println("\nWARNING: Restore will replace the current database and storage files.")
		fmt.Println("The archived configuration is preserved, not applied.")
		fmt.Print("Are you sure? (type 'yes' to confirm): ")
		var confirmation string
		fmt.Scanln(&confirmation)
		if confirmation != "yes" {
			fmt.Println("Restore cancelled.")
			return nil
		}
	}

	tempParent := tempDirOverride
	if tempParent == "" {
		tempParent = filepath.Dir(c.cfg.Load().Storage.StorageDir)
	}
	if err := os.MkdirAll(tempParent, 0755); err != nil {
		return fmt.Errorf("create temp parent: %w", err)
	}
	tmpDir, err := os.MkdirTemp(tempParent, "edub-restore-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := checkRestoreDiskSpace(tempParent, filepath.Dir(c.cfg.Load().Storage.StorageDir), manifest); err != nil {
		return err
	}

	fmt.Println("Extracting archive...")
	if err := backup.ExtractArchive(archivePath, tmpDir); err != nil {
		return fmt.Errorf("extract archive: %w", err)
	}

	db, err := c.GetDB()
	if err != nil {
		return err
	}
	cfg := c.cfg.Load()
	configPath := filepath.Join(cfg.App.ConfigDir, "config.yaml")

	fmt.Println("Replacing files...")
	if err := backup.ReplaceFiles(tmpDir, db, cfg.Db, config.BuildPostgresDSN(cfg.Db), configPath, cfg.Storage.StorageDir); err != nil {
		return fmt.Errorf("replace files: %w", err)
	}

	fmt.Println("Restore completed successfully.")
	fmt.Printf("Restored config saved as %s — the archived storage_dir may not match rewritten paths; update before applying.\n", configPath+".restored")
	fmt.Println("Restart the services to pick up the restored data.")

	return nil
}

const gib = 1024 * 1024 * 1024

func checkRestoreDiskSpace(tempParent, storageParent string, manifest *backup.Manifest) error {
	mode := manifest.Mode
	if mode == "" {
		mode = backup.BackupModeFull
	}

	var required int64
	switch mode {
	case backup.BackupModeDatabase:
		if manifest.DbSizeBytes <= 0 {
			return fmt.Errorf("manifest is corrupt or hand-made: db_size_bytes = %d — refusing to restore", manifest.DbSizeBytes)
		}
		required = manifest.DbSizeBytes
	case backup.BackupModeDocuments:
		if manifest.StorageSizeBytes <= 0 {
			return fmt.Errorf("manifest is corrupt or hand-made: storage_size_bytes = %d — refusing to restore", manifest.StorageSizeBytes)
		}
		required = manifest.StorageSizeBytes
	default:
		if manifest.DbSizeBytes <= 0 || manifest.StorageSizeBytes <= 0 {
			return fmt.Errorf("manifest is corrupt or hand-made: db_size_bytes = %d, storage_size_bytes = %d — refusing to restore", manifest.DbSizeBytes, manifest.StorageSizeBytes)
		}
		required = manifest.DbSizeBytes + manifest.StorageSizeBytes
	}

	need := uint64(float64(required) * 1.05)

	var st syscall.Statfs_t
	if err := syscall.Statfs(tempParent, &st); err != nil {
		return fmt.Errorf("statfs %s: %w", tempParent, err)
	}
	free := st.Bavail * uint64(st.Bsize)
	if free < need {
		return fmt.Errorf("insufficient disk space on %s: restore needs ~%.1f GB, %.1f GB available — free space or pass --temp-dir <dir with at least %.1f GB free>",
			tempParent, float64(required)/gib, float64(free)/gib, float64(need)/gib)
	}

	onSameDevice, err := utils.SameDevice(tempParent, storageParent)
	if err != nil {
		return fmt.Errorf("stat %s: %w", storageParent, err)
	}
	if !onSameDevice {
		var st2 syscall.Statfs_t
		if err := syscall.Statfs(storageParent, &st2); err != nil {
			return fmt.Errorf("statfs %s: %w", storageParent, err)
		}
		storageNeed := uint64(float64(manifest.StorageSizeBytes) * 1.05)
		free2 := st2.Bavail * uint64(st2.Bsize)
		if free2 < storageNeed {
			return fmt.Errorf("insufficient disk space on %s: storage copy needs ~%.1f GB, %.1f GB available — free space or pass --temp-dir <dir on the same filesystem as %s>",
				storageParent, float64(manifest.StorageSizeBytes)/gib, float64(free2)/gib, storageParent)
		}
	}

	return nil
}
