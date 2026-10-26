package cli

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/wgomg/edub-kushim/internal/backup"
	"github.com/wgomg/edub-kushim/internal/ops/consume"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

func mirrorHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help("Usage: kushim mirror [--from-batch <id> --exec-token <id>] [--path <dest>] [--no-wait]\n" +
		"  Mirror the storage tree to a destination with rsync --delete, or execute\n" +
		"  one claimed mirror batch (--from-batch, dispatcher and 'kushim batch run' contract).\n\n" +
		"  --path     Override destination (default: config mirror.path)\n" +
		"             Local path or rsync remote target ([user@]host:path)\n" +
		"  --no-wait  Exit non-zero instead of waiting for a lock or in-flight work\n\n" +
		"  To enqueue a mirror: kushim batch create mirror [--path <dest>]") {
		return nil
	}

	var batchID, execTokenParam string
	if err := fp.String("--from-batch", &batchID); err != nil {
		return err
	}
	if err := fp.String("--exec-token", &execTokenParam); err != nil {
		return err
	}
	var overridePath string
	fp.String("--path", &overridePath)
	noWait := false
	fp.Bool("--no-wait", &noWait)
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown arguments: %v", rest)
	}

	if batchID != "" || execTokenParam != "" {
		if batchID == "" {
			return fmt.Errorf("--exec-token requires --from-batch <id>")
		}
		if overridePath != "" || noWait {
			return fmt.Errorf("--from-batch cannot be combined with the direct-mode flags")
		}
		return runMirrorBatch(c, batchID, execTokenParam)
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

// runMirrorBatch executes one claimed mirror batch in this child. The dispatcher claim owns the maintenance
// hold and waits for storage; the handler runs the rsync action and records the result on the task row.
func runMirrorBatch(c *container.Container, batchID, execTokenParam string) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	store := task.NewStore(client)
	registry := task.NewRegistry()
	registry.Register(types.Task.Type.Mirror,
		backup.NewMirrorTaskHandler(client.Queries, c.GetConfig, c.GetLogger()))

	_, err = consume.RunClaimedBatch(c, batchID, execTokenParam, store, registry, nil)
	return err
}
