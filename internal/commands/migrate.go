package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wgomg/edub-kushim/internal/configtask"
)

func migrateHandler(c *Container, args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: kushim migrate <database|storage>\n" +
			"  Migrate the database or the storage directories to new locations.\n\n" +
			"  kushim migrate database    Copy the database to a new PostgreSQL server\n" +
			"  kushim migrate storage     Relocate storage and consumption directories")
		return nil
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

const migrateDatabaseUsage = "Usage: kushim migrate database --host <h> --port <p> --user <u> --password <p> --database <db> [--sslmode <mode>]\n" +
	"  Copy the current database to a new PostgreSQL server and point config.yaml at it.\n\n" +
	"  --host       Destination host (required)\n" +
	"  --port       Destination port (required)\n" +
	"  --user       Destination user (required)\n" +
	"  --password   Destination password (required, or set KUSHIM_DB_PASSWORD)\n" +
	"  --database   Destination database name (required)\n" +
	"  --sslmode    Destination SSL mode (default: disable)"

func migrateDatabaseHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)
	if fp.Help(migrateDatabaseUsage) {
		return nil
	}

	var host, port, user, password, database, sslMode string
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
	if err := fp.String("--database", &database); err != nil {
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

	if host == "" || port == "" || user == "" || database == "" {
		fmt.Fprintln(os.Stderr, migrateDatabaseUsage)
		return fmt.Errorf("--host, --port, --user and --database are required (--password may be set via KUSHIM_DB_PASSWORD)")
	}
	if sslMode == "" {
		sslMode = "disable"
	}

	payload := configtask.MigrateDBPayload{
		ConfigDir: c.cfg.Load().App.ConfigDir,
		Host:      host,
		Port:      port,
		User:      user,
		Password:  password,
		Database:  database,
		SSLMode:   sslMode,
	}

	fmt.Printf("Migrating database to %s@%s:%s/%s...\n", user, host, port, database)
	if err := configtask.MigrateDatabase(context.Background(), c.logger, payload); err != nil {
		return err
	}
	fmt.Printf("Database %q is live and config.yaml updated.\n", database)
	return nil
}

const migrateStorageUsage = "Usage: kushim migrate storage --storage-dir <new> [--consumption-dir <new>]\n" +
	"  Relocate the storage and consumption directories and update config.yaml.\n" +
	"  At least one of --storage-dir or --consumption-dir must be provided.\n\n" +
	"  --storage-dir        New storage directory\n" +
	"  --consumption-dir    New consumption (inbox) directory"

func migrateStorageHandler(c *Container, args []string) error {
	fp := NewFlagParser(args)
	if fp.Help(migrateStorageUsage) {
		return nil
	}

	var newStorage, newConsumption string
	if err := fp.String("--storage-dir", &newStorage); err != nil {
		return err
	}
	if err := fp.String("--consumption-dir", &newConsumption); err != nil {
		return err
	}
	if rest := fp.Rest(); len(rest) > 0 {
		return fmt.Errorf("unknown arguments: %v", rest)
	}

	if newStorage == "" && newConsumption == "" {
		fmt.Fprintln(os.Stderr, migrateStorageUsage)
		return fmt.Errorf("at least one of --storage-dir or --consumption-dir is required")
	}

	if err := rejectUnsafePath(newStorage, "--storage-dir"); err != nil {
		return err
	}
	if err := rejectUnsafePath(newConsumption, "--consumption-dir"); err != nil {
		return err
	}

	cfg := c.cfg.Load()
	payload := configtask.MigrateStoragePayload{ConfigDir: cfg.App.ConfigDir}
	changed := false
	if configtask.DirChanged(cfg.Storage.StorageDir, newStorage) {
		payload.OldStorageDir = cfg.Storage.StorageDir
		payload.NewStorageDir = newStorage
		changed = true
	}
	if configtask.DirChanged(cfg.Storage.ConsumptionDir, newConsumption) {
		payload.OldConsumptionDir = cfg.Storage.ConsumptionDir
		payload.NewConsumptionDir = newConsumption
		changed = true
	}
	if !changed {
		fmt.Println("No migration needed: requested directories already match the current config.")
		return nil
	}

	fmt.Println("Migrating storage directories...")
	if err := configtask.MigrateStorage(context.Background(), c.logger, payload); err != nil {
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
