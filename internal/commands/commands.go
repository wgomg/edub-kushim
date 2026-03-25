package commands

import (
	"fmt"
	"os"
)

type Command struct {
	Name        string
	Description string
	Handler     func(container *Container, args []string) error
}

var commands = map[string]Command{
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
}

type CommandRunner struct {
	container *Container
	commands  map[string]Command
}

func NewCommandRunner(container *Container) *CommandRunner {
	return &CommandRunner{
		container: container,
		commands:  commands,
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
	for _, cmd := range commands {
		cmdList = append(cmdList, cmd)
	}
	return cmdList
}

func PrintUsage() {
	fmt.Println("Usage: cli <command> [arguments]")
	fmt.Println("\nAvailable commands:")
	for _, cmd := range commands {
		fmt.Printf("  %-15s %s\n", cmd.Name, cmd.Description)
	}
	os.Exit(1)
}

func versionHandler(container *Container, args []string) error {
	fmt.Println("Document Management System v0.1.0")
	return nil
}
