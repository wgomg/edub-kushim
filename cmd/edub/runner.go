package main

import (
	"fmt"
	"os"

	"github.com/wgomg/edub-kushim/internal/version"
)

var commands = map[string]func(args []string) error{
	"version": func(args []string) error {
		fmt.Printf("Document Management System v%s\n", version.Version)
		return nil
	},
}

func Execute(command string, args []string) error {
	handler, exists := commands[command]
	if !exists {
		return fmt.Errorf("unknown command: %s", command)
	}
	return handler(args)
}

func printUsage() {
	fmt.Println("Usage: edub [command]")
	fmt.Println("\nCommands:")
	for name := range commands {
		fmt.Printf("  %-15s %s\n", name, commandDescriptions[name])
	}
	fmt.Println("\nFlags:")
	fmt.Println("  --help, -h        Print this help message")
	os.Exit(1)
}

var commandDescriptions = map[string]string{
	"version": "Show application version",
}
