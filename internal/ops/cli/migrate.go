package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/ops/configtask"
	"github.com/wgomg/edub-kushim/internal/ops/consume"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

func migrateHandler(c *container.Container, args []string) error {
	if len(args) == 0 {
		fmt.Println(migrateUsage)
		return nil
	}

	if strings.HasPrefix(args[0], "-") {
		return migrateBatchHandler(c, args)
	}

	switch args[0] {
	case "database":
		return migrateDatabaseHandler(c, args[1:])
	case "storage":
		return migrateStorageHandler(c, args[1:])
	default:
		return fmt.Errorf("unknown migrate subcommand: %s (use 'database' or 'storage')", args[0])
	}
}

const migrateUsage = "Usage: kushim migrate <database|storage> [flags]\n" +
	"       kushim migrate --from-batch <id> --exec-token <id>\n" +
	"  Migrate the database or the storage directories to new locations.\n\n" +
	"  kushim migrate database    Copy the database to a new PostgreSQL server\n" +
	"  kushim migrate storage     Relocate storage and consumption directories\n\n" +
	"  --from-batch <id>  execute one claimed migration batch (dispatcher and 'kushim batch run' contract);\n" +
	"                     both migration task types run through this one mode\n" +
	"  --exec-token <id>  execution token passed with --from-batch"

// migrateBatchHandler executes one claimed migration batch: the batch carries migrate-db or migrate-storage
// tasks, and the child registers both handlers. The lease and every settle write stay in the source
// database: the migrate-db handler only rewrites config.yaml, so the command's client is never switched
// mid-run.
func migrateBatchHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	var batchID, execTokenParam string
	if err := fp.String("--from-batch", &batchID); err != nil {
		return err
	}
	if err := fp.String("--exec-token", &execTokenParam); err != nil {
		return err
	}
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown argument: %s", rest[0])
	}
	if batchID == "" {
		return fmt.Errorf("--from-batch <id> is required: run 'kushim batch run <id>' to execute a queued migration")
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	store := task.NewStore(client)
	registry := task.NewRegistry()
	registry.Register(types.Task.Type.MigrateDB, configtask.NewMigrateDBTaskHandler(client.Queries, c.GetLogger()))
	registry.Register(types.Task.Type.MigrateStorage, configtask.NewMigrateStorageTaskHandler(client.Queries, c.GetLogger()))

	_, err = consume.RunClaimedBatch(c, batchID, execTokenParam, store, registry, nil)
	return err
}

const migrateDatabaseUsage = "Usage: kushim migrate database --host <h> --port <p> --user <u> --password <p> --database <db> [--sslmode <mode>]\n" +
	"  Copy the current database to a new PostgreSQL server and point config.yaml at it.\n\n" +
	"  --host       Destination host (required)\n" +
	"  --port       Destination port (required)\n" +
	"  --user       Destination user (required)\n" +
	"  --password   Destination password (required, or set KUSHIM_DB_PASSWORD)\n" +
	"  --database   Destination database name (required)\n" +
	"  --sslmode    Destination SSL mode (default: disable)"

func migrateDatabaseHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(migrateDatabaseUsage) {
		return nil
	}

	var host, port, user, password, databaseName, sslMode string
	if err := fp.String("--host", &host); err != nil {
		return err
	}
	if err := fp.String("--port", &port); err != nil {
		return err
	}
	if err := fp.String("--user", &user); err != nil {
		return err
	}
	if err := fp.String("--password", &password); err != nil {
		return err
	}
	if err := fp.String("--database", &databaseName); err != nil {
		return err
	}
	if err := fp.String("--sslmode", &sslMode); err != nil {
		return err
	}
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown arguments: %v", rest)
	}

	if password == "" {
		password = os.Getenv("KUSHIM_DB_PASSWORD")
	}

	if host == "" || port == "" || user == "" || databaseName == "" {
		_, _ = fmt.Fprintln(os.Stderr, migrateDatabaseUsage)
		return fmt.Errorf("--host, --port, --user and --database are required (--password may be set via KUSHIM_DB_PASSWORD)")
	}
	if sslMode == "" {
		sslMode = "disable"
	}

	payload := configtask.MigrateDBPayload{
		ConfigDir: c.GetConfig().App.ConfigDir,
		Host:      host,
		Port:      port,
		User:      user,
		Password:  password,
		Database:  databaseName,
		SSLMode:   sslMode,
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	policy := task.DirectLockPolicyFor(types.Task.Type.MigrateDB)
	lockToken := uuid.NullUUID{}
	if err := task.AcquireLocks(ctx, client, policy.Holds, policy.WaitsFor, lockToken, ""); err != nil {
		if errors.Is(err, task.ErrLockBusy) {
			return fmt.Errorf("a maintenance run is in progress — wait for it to finish")
		}
		return fmt.Errorf("acquire maintenance lock: %w", err)
	}
	defer func() {
		relCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := task.ReleaseLocksForToken(relCtx, client.Queries, lockToken); err != nil {
			c.GetLogger().Error("", "release maintenance lock: %v", err)
		}
	}()
	stopHeartbeat := database.StartLockHeartbeat(ctx, client.Queries, c.GetLogger(), task.LockHeartbeatInterval, policy.Holds, lockToken)
	defer stopHeartbeat()

	if err := database.WaitForTaskDrain(ctx, client.Queries, c.GetLogger(), "migrate-db",
		types.TaskTypesWaitingOn(policy.Holds...)); err != nil {
		return err
	}

	fmt.Printf("Migrating database to %s@%s:%s/%s...\n", user, host, port, databaseName)
	if err := configtask.MigrateDatabase(ctx, c.GetLogger(), payload, task.NoopProgress); err != nil {
		return err
	}
	fmt.Printf("Database %q is live and config.yaml updated.\n", databaseName)
	return nil
}

const migrateStorageUsage = "Usage: kushim migrate storage --storage-dir <new> [--consumption-dir <new>]\n" +
	"  Relocate the storage and consumption directories and update config.yaml.\n" +
	"  At least one of --storage-dir or --consumption-dir must be provided.\n\n" +
	"  --storage-dir        New storage directory\n" +
	"  --consumption-dir    New consumption (inbox) directory"

func migrateStorageHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help(migrateStorageUsage + "\n\n  --no-wait  Exit non-zero instead of waiting for a lock or in-flight work") {
		return nil
	}

	var newStorage, newConsumption string
	if err := fp.String("--storage-dir", &newStorage); err != nil {
		return err
	}
	if err := fp.String("--consumption-dir", &newConsumption); err != nil {
		return err
	}
	noWait := false
	fp.Bool("--no-wait", &noWait)
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown arguments: %v", rest)
	}

	if newStorage == "" && newConsumption == "" {
		_, _ = fmt.Fprintln(os.Stderr, migrateStorageUsage)
		return fmt.Errorf("at least one of --storage-dir or --consumption-dir is required")
	}

	if err := rejectUnsafePath(newStorage, "--storage-dir"); err != nil {
		return err
	}
	if err := rejectUnsafePath(newConsumption, "--consumption-dir"); err != nil {
		return err
	}

	cfg := c.GetConfig()
	payload, changed := configtask.StorageMigrationPayload(cfg, newStorage, newConsumption)
	if !changed {
		fmt.Println("No migration needed: requested directories already match the current config.")
		return nil
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fmt.Println("Migrating storage directories...")
	policy := task.DirectLockPolicyFor(types.Task.Type.MigrateStorage)
	err = task.RunDirect(ctx, client, c.GetLogger(), "migrate-storage", policy, !noWait, nil, func() error {
		return configtask.MigrateStorage(ctx, c.GetLogger(), payload, task.NoopProgress)
	})
	if err != nil {
		return err
	}
	fmt.Println("Storage files relocated and config.yaml updated.")
	return nil
}

var unsafePathPrefixes = []string{"/proc", "/sys", "/dev", "/boot"}

func rejectUnsafePath(p, flag string) error {
	if p == "" {
		return nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("%s: resolve path: %w", flag, err)
	}
	cleaned := filepath.Clean(abs)
	for _, prefix := range unsafePathPrefixes {
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+string(filepath.Separator)) {
			return fmt.Errorf("%s=%q resolves to %q, which is a system location and is not allowed", flag, p, cleaned)
		}
	}
	return nil
}
