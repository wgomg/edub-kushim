// Package cli is the CLI verb layer: the verb tables (cli and server
// sets), the parser and dispatcher over the composition root, and the
// usage printers. cmd/kushim dispatches through here.
package cli

import (
	"fmt"
	"os"

	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/consume"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/queue"
	"github.com/wgomg/edub-kushim/internal/ops/setup"
	"github.com/wgomg/edub-kushim/internal/ops/tagmaintenance"
	"github.com/wgomg/edub-kushim/internal/version"
)

// Command is one CLI verb: its name, description and handler over the
// container.
type Command struct {
	Name        string
	Description string
	Handler     func(c *container.Container, args []string) error
}

var commandSets = map[string]map[string]Command{
	"cli": {
		"version": {
			Name:        "version",
			Description: "Show application version",
			Handler:     versionHandler,
		},
		"consume": {
			Name:        "consume",
			Description: "Process documents from consumption directory",
			Handler:     consume.Run,
		},
		"search": {
			Name:        "search",
			Description: "Full-text search across documents",
			Handler:     searchHandler,
		},
		"hugot": {
			Name:        "hugot",
			Description: "Start the matcher RPC server over a Unix socket",
			Handler:     serveHugotHandler,
		},
		"task": {
			Name:        "task",
			Description: "Manage tasks (list, status)",
			Handler:     taskHandler,
		},
		"batch": {
			Name:        "batch",
			Description: "Create and manage batches (create, run, stop, resume, list, status)",
			Handler:     batchHandler,
		},
		"user": {
			Name:        "user",
			Description: "Manage users (create)",
			Handler:     userHandler,
		},
		"setup": {
			Name:        "setup",
			Description: "Initialize config and download OCR language files (run once). Use --cli for terminal-based setup.",
			Handler: func(_ *container.Container, args []string) error {
				return setup.RunSetup(args, log.NewLogger("info"))
			},
		},
		"enrich": {
			Name:        "enrich",
			Description: "Re-run enrichment for one or more documents",
			Handler:     enrichHandler,
		},
		"thumbnails": {
			Name:        "thumbnails",
			Description: "Backfill missing thumbnails",
			Handler:     backfillThumbnailsHandler,
		},
		"queue": {
			Name:        "queue",
			Description: "Start the batch queue daemon for background consumption",
			Handler:     queue.Run,
		},
		"storage": {
			Name:        "storage",
			Description: "Manage storage: detect and quarantine orphaned files",
			Handler:     storageHandler,
		},
		"backup": {
			Name:        "backup",
			Description: "Create a backup of database, config, and storage files",
			Handler:     backupHandler,
		},
		"mirror": {
			Name:        "mirror",
			Description: "Mirror the storage tree to a destination with rsync --delete",
			Handler:     mirrorHandler,
		},
		"config": {
			Name:        "config",
			Description: "View and edit config values",
			Handler:     configHandler,
		},
		"llm": {
			Name:        "llm",
			Description: "View and edit the provider and model catalogs",
			Handler:     llmHandler,
		},
		"restore": {
			Name:        "restore",
			Description: "Restore from a backup archive",
			Handler:     restoreHandler,
		},
		"migrate": {
			Name:        "migrate",
			Description: "Migrate database or storage to new locations",
			Handler:     migrateHandler,
		},
		"tagmeasure": {
			Name:        "tagmeasure",
			Description: "Measure the tag store's extension algebra into versioned evidence records",
			Handler:     tagMeasureHandler,
		},
		"tagreconcile": {
			Name:        "tagreconcile",
			Description: "Re-adjudicate stored tag evidence: re-check pairs, split compounds, canonicalize, sweep",
			Handler:     tagReconcileHandler,
		},
		"tagidentity": {
			Name:        "tagidentity",
			Description: "Adjudicate pending tag identity evidence and apply its dispositions",
			Handler:     tagIdentityHandler,
		},
		"tagaudit": {
			Name:        "tagaudit",
			Description: "Audit document coverage against the tag vocabulary, verifying and repairing application recall",
			Handler:     tagAuditHandler,
		},
		"tagmaintenance": {
			Name:        "tagmaintenance",
			Description: "Run the tag maintenance routine: reconcile, identity, audit and measure",
			Handler:     tagmaintenance.Run,
		},
	},
	"server": {
		"version": {
			Name:        "version",
			Description: "Show application version",
			Handler:     versionHandler,
		},
	},
}

// CommandRunner dispatches a verb invocation to the table.
type CommandRunner struct {
	container *container.Container
	commands  map[string]Command
}

// NewCommandRunner selects the verb table by set (cli or server) and
// returns the dispatcher.
func NewCommandRunner(c *container.Container, set string) *CommandRunner {
	cmds := commandSets["cli"]
	if s, ok := commandSets[set]; ok {
		cmds = s
	}
	return &CommandRunner{
		container: c,
		commands:  cmds,
	}
}

// ExecuteCommand looks up the verb and runs it. Mutual exclusion between
// instances is per verb: the queue daemon gates itself through its PID
// file, and consume through the batch-ownership protocol.
func (r *CommandRunner) ExecuteCommand(name string, args []string) error {
	cmd, exists := r.commands[name]
	if !exists {
		return fmt.Errorf("unknown command: %s", name)
	}
	return cmd.Handler(r.container, args)
}

// PrintUsage prints the CLI verb table and the global flags.
func PrintUsage() {
	fmt.Println("Usage: kushim <command> [arguments]")
	fmt.Println("\nAvailable commands:")
	for _, cmd := range commandSets["cli"] {
		fmt.Printf("  %-15s %s\n", cmd.Name, cmd.Description)
	}
	fmt.Println("\nUse 'kushim <command> --help' for command-specific help.")
	os.Exit(1)
}

// PrintServerUsage prints the server command's usage.
func PrintServerUsage() {
	fmt.Println("Usage: edub [command]")
	fmt.Println("\nCommands:")
	for _, cmd := range commandSets["server"] {
		fmt.Printf("  %-15s %s\n", cmd.Name, cmd.Description)
	}
	fmt.Println("\nFlags:")
	fmt.Println("  --help, -h        Print this help message")
	os.Exit(1)
}

func versionHandler(c *container.Container, args []string) error {
	fmt.Printf("Document Management System v%s\n", version.Version)
	return nil
}
