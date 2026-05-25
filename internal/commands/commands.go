package commands

import (
	"fmt"
	"os"

	"github.com/wgomg/edub-kushim/internal/version"
)

type Command struct {
	Name        string
	Description string
	Handler     func(container *Container, args []string) error
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
			Handler:     consumeHandler,
		},
		"search": {
			Name:        "search",
			Description: "Full-text search across documents",
			Handler:     searchHandler,
		},
		"task": {
			Name:        "task",
			Description: "Manage tasks (list, status, retry)",
			Handler:     taskHandler,
		},
		"setup": {
			Name:        "setup",
			Description: "Initialize config and download OCR language files (run once)",
			Handler:     setupHandler,
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

type CommandRunner struct {
	container *Container
	commands  map[string]Command
}

func NewCommandRunner(container *Container, set string) *CommandRunner {
	cmds := commandSets["cli"]
	if s, ok := commandSets[set]; ok {
		cmds = s
	}
	return &CommandRunner{
		container: container,
		commands:  cmds,
	}
}

func (r *CommandRunner) ExecuteCommand(name string, args []string) error {
	cmd, exists := r.commands[name]
	if !exists {
		return fmt.Errorf("unknown command: %s", name)
	}
	return cmd.Handler(r.container, args)
}

func ListCommands() []Command {
	var cmdList []Command
	for _, cmd := range commandSets["cli"] {
		cmdList = append(cmdList, cmd)
	}
	return cmdList
}

func PrintUsage() {
	fmt.Println("Usage: kushim <command> [arguments]")
	fmt.Println("\nAvailable commands:")
	for _, cmd := range commandSets["cli"] {
		fmt.Printf("  %-15s %s\n", cmd.Name, cmd.Description)
	}
	fmt.Println("\nUse 'kushim <command> --help' for command-specific help.")
	os.Exit(1)
}

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

func versionHandler(container *Container, args []string) error {
	fmt.Printf("Document Management System v%s\n", version.Version)
	return nil
}
