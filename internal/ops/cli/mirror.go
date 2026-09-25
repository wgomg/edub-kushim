package cli

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
)

func mirrorHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help("Usage: kushim mirror [--path <dest>]\n" +
		"  Mirror the storage tree to a destination with rsync --delete.\n\n" +
		"  --path    Override destination (default: config mirror.path)\n" +
		"            Local path or rsync remote target ([user@]host:path)") {
		return nil
	}

	var overridePath string
	fp.String("--path", &overridePath)
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown arguments: %v", rest)
	}

	if !backup.Available() {
		return fmt.Errorf("rsync is not installed — install it (e.g. 'sudo apt install rsync') to use the mirror feature")
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Println("Waiting for the backup lock...")
	if err := database.AcquireMaintenanceLock(ctx, client.Queries, c.GetLogger(), "Backup"); err != nil {
		return err
	}
	defer client.Queries.ReleaseBackupLock(context.Background(), uuid.NullUUID{})

	cfg := c.GetConfig()
	dest := cfg.Mirror.Path
	if overridePath != "" {
		dest = overridePath
	}
	if dest == "" {
		return fmt.Errorf("mirror.path is not configured — set it in the config or pass --path")
	}

	if err := backup.ValidateDestination(dest, cfg.Storage.StorageDir, cfg.Backup.Path); err != nil {
		return err
	}

	fmt.Printf("Mirroring %s -> %s\n", cfg.Storage.StorageDir, dest)
	result, _, err := backup.RunLocked(ctx, client.Queries, c.GetLogger(), cfg.Storage.StorageDir, dest)
	if err != nil {
		return fmt.Errorf("mirror failed: %w", err)
	}

	fmt.Printf("Mirror completed: %s\n", result.Summary())
	fmt.Printf("State file: %s/.edub-mirror.json\n", dest)
	return nil
}
