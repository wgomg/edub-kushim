package commands

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/task"
)

type orphanAction func(ctx context.Context, id int64) error
type orphanBulkAction func(ctx context.Context) (int, error)

func storageHandler(c *Container, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: kushim storage <subcommand> [arguments]")
		fmt.Println("\nSubcommands:")
		fmt.Println("  orphans ...              Manage orphaned files")
		fmt.Println("\nUse 'kushim storage <subcommand> --help' for subcommand-specific help.")
		return nil
	}

	switch args[0] {
	case "orphans":
		return orphansHandler(c, args[1:])
	default:
		return fmt.Errorf("unknown storage subcommand: %s", args[0])
	}
}

func orphansHandler(c *Container, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: kushim storage orphans <action>")
		fmt.Println("\nActions:")
		fmt.Println("  list                    List pending orphaned files")
		fmt.Println("  scan                    Run detection and quarantine")
		fmt.Println("  delete --id <n>         Delete a specific orphaned file")
		fmt.Println("  restore --id <n>        Restore a specific orphaned file (uuid only)")
		fmt.Println("  move-to-inbox --id <n>  Move a specific orphaned file to inbox")
		fmt.Println("  delete-all              Delete all pending orphaned files")
		fmt.Println("  move-to-inbox-all       Move all pending orphaned files to inbox")
		return nil
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	store := task.NewStore(client.Queries)
	batchSvc := service.NewBatch(client, c.cfg.Load().Consumer.Reclaim.MaxRetries)
	svc := service.NewOrphaned(client.Queries, c.cfg.Load(), c.logger, store, batchSvc)

	action := args[0]
	remaining := args[1:]

	switch action {
	case "list":
		return orphansList(svc, remaining)
	case "scan":
		return orphansScan(svc, remaining)
	case "delete":
		return parseAndRun(svc, remaining, svc.Delete)
	case "restore":
		return parseAndRun(svc, remaining, svc.Restore)
	case "move-to-inbox":
		return parseAndRunOrAll(svc, remaining, svc.MoveToInbox, svc.MoveAllToInbox)
	case "delete-all":
		return orphansBulk(svc, remaining, "delete", svc.DeleteAll)
	case "move-to-inbox-all":
		return orphansBulk(svc, remaining, "move to inbox", svc.MoveAllToInbox)
	default:
		return fmt.Errorf("unknown orphans action: %s", action)
	}
}

func parseAndRun(svc *service.Orphaned, args []string, action orphanAction) error {
	fp := NewFlagParser(args)
	if fp.Help("") {
		return nil
	}

	var idStr string
	fp.String("--id", &idStr)

	if idStr == "" {
		return fmt.Errorf("--id <n> is required")
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid --id value: %s", idStr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := action(ctx, id); err != nil {
		return fmt.Errorf("action failed for id %d: %w", id, err)
	}

	fmt.Printf("Orphaned file %d processed successfully.\n", id)
	return nil
}

func parseAndRunOrAll(svc *service.Orphaned, args []string, single orphanAction, bulk orphanBulkAction) error {
	fp := NewFlagParser(args)
	if fp.Help("") {
		return nil
	}

	var idStr string
	fp.String("--id", &idStr)
	var all bool
	fp.Bool("--all", &all)

	if idStr == "" && !all {
		return fmt.Errorf("--id <n> or --all is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if all {
		count, err := bulk(ctx)
		if err != nil {
			return fmt.Errorf("bulk action: %w", err)
		}
		fmt.Printf("%d orphaned file(s) processed.\n", count)
		return nil
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid --id value: %s", idStr)
	}

	if err := single(ctx, id); err != nil {
		return fmt.Errorf("action failed for id %d: %w", id, err)
	}

	fmt.Printf("Orphaned file %d processed successfully.\n", id)
	return nil
}

func orphansBulk(svc *service.Orphaned, args []string, label string, action orphanBulkAction) error {
	fp := NewFlagParser(args)
	if fp.Help("") {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	count, err := action(ctx)
	if err != nil {
		return fmt.Errorf("bulk %s: %w", label, err)
	}

	fmt.Printf("%d orphaned file(s) %sd.\n", count, label)
	return nil
}

func orphansList(svc *service.Orphaned, args []string) error {
	fp := NewFlagParser(args)
	if fp.Help("Usage: kushim storage orphans list\n  List pending orphaned files.") {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	files, err := svc.List(ctx)
	if err != nil {
		return fmt.Errorf("list orphaned files: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No orphaned files found.")
		return nil
	}

	fmt.Printf("Found %d orphaned file(s):\n\n", len(files))
	for _, f := range files {
		actionType := ""
		if f.ActionType.Valid {
			actionType = f.ActionType.String
		}
		actionAt := ""
		if f.ActionAt.Valid {
			actionAt = f.ActionAt.Time.Format(time.RFC3339)
		}
		detectedAt := ""
		if f.DetectedAt.Valid {
			detectedAt = f.DetectedAt.Time.Format(time.RFC3339)
		}

		keyStr := fmt.Sprintf("%s (%s)", f.DocumentKey, f.DocumentKeyType)

		statusStr := f.Status
		if statusStr != "pending" {
			statusStr = fmt.Sprintf("%s (at %s: %s)", f.Status, actionAt, actionType)
		}

		fmt.Printf("  ID: %d\n", f.ID)
		fmt.Printf("  Key: %s\n", keyStr)
		fmt.Printf("  Source: %s\n", f.SourceDir)
		fmt.Printf("  Path: %s\n", f.OriginalPath)
		fmt.Printf("  Size: %d bytes\n", f.FileSize)
		fmt.Printf("  Status: %s\n", statusStr)
		if detectedAt != "" {
			fmt.Printf("  Detected: %s\n", detectedAt)
		}
		fmt.Println()
	}

	return nil
}

func orphansScan(svc *service.Orphaned, args []string) error {
	fp := NewFlagParser(args)
	if fp.Help("Usage: kushim storage orphans scan\n  Run detection and quarantine.") {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Println("Scanning for orphaned files...")
	count, err := svc.ScanAndQuarantine(ctx)
	if err != nil {
		return fmt.Errorf("scan orphaned files: %w", err)
	}

	fmt.Printf("Done. %d file(s) quarantined.\n", count)
	return nil
}
