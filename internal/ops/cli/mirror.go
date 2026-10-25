package cli

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

func mirrorHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help("Usage: kushim mirror [--path <dest>] [--no-wait]\n" +
		"  Mirror the storage tree to a destination with rsync --delete.\n\n" +
		"  --path     Override destination (default: config mirror.path)\n" +
		"             Local path or rsync remote target ([user@]host:path)\n" +
		"  --no-wait  Exit non-zero instead of waiting for a lock or in-flight work") {
		return nil
	}

	var overridePath string
	fp.String("--path", &overridePath)
	noWait := false
	fp.Bool("--no-wait", &noWait)
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
	var result *backup.Result
	policy := task.DirectLockPolicyFor(types.Task.Type.Mirror)
	err = task.RunDirect(ctx, client, c.GetLogger(), "mirror", policy, !noWait, nil, func() error {
		var runErr error
		result, _, runErr = backup.RunMirrorAndRecord(ctx, c.GetLogger(), cfg.Storage.StorageDir, dest, nil)
		return runErr
	})
	if err != nil {
		return err
	}

	fmt.Printf("Mirror completed: %s\n", result.Summary())
	fmt.Printf("State file: %s/.edub-mirror.json\n", dest)
	return nil
}
