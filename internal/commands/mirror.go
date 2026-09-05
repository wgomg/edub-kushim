package commands

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/mirror"
)

func mirrorHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)
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

	if !mirror.Available() {
		return fmt.Errorf("rsync is not installed — install it (e.g. 'sudo apt install rsync') to use the mirror feature")
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Println("Waiting for the backup lock...")
	if err := waitForBackupLock(ctx, client); err != nil {
		return err
	}
	defer client.Queries.ReleaseBackupLock(context.Background())

	cfg := c.cfg.Load()
	dest := cfg.Mirror.Path
	if overridePath != "" {
		dest = overridePath
	}
	if dest == "" {
		return fmt.Errorf("mirror.path is not configured — set it in the config or pass --path")
	}

	if err := mirror.ValidateDestination(dest, cfg.Storage.StorageDir, cfg.Backup.Path); err != nil {
		return err
	}

	fmt.Printf("Mirroring %s -> %s\n", cfg.Storage.StorageDir, dest)
	result, _, err := mirror.RunLocked(ctx, client.Queries, c.logger, cfg.Storage.StorageDir, dest)
	if err != nil {
		return fmt.Errorf("mirror failed: %w", err)
	}

	fmt.Printf("Mirror completed: %d files, %d bytes\n", result.Files, result.Bytes)
	fmt.Printf("State file: %s/.edub-mirror.json\n", dest)
	return nil
}

func waitForBackupLock(ctx context.Context, client *database.Client) error {
	for {
		rows, err := client.Queries.AcquireBackupLock(ctx)
		if err != nil {
			return fmt.Errorf("acquire backup lock: %w", err)
		}
		if rows > 0 {
			return nil
		}
		fmt.Println("Backup lock held by another process — waiting...")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
