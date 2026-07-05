package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/wgomg/edub-kushim/internal/auth"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/service"
)

func userHandler(c *Container, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: kushim user <subcommand>\n\n" +
			"Subcommands:\n" +
			"  create            create a user (--username, --password, --role)")
		if len(args) == 0 {
			return fmt.Errorf("user requires a subcommand")
		}
		return nil
	}

	switch args[0] {
	case "create":
		return userCreateHandler(c, args[1:])
	default:
		return fmt.Errorf("unknown user subcommand: %s", args[0])
	}
}

func userCreateHandler(c *Container, args []string) error {
	var username, password, role string

	fp := NewFlagParser(args)

	if fp.Help("Usage: kushim user create [--username <name>] [--password <secret>] [--role <admin|editor|viewer>]\n\n" +
		"  Create a user with the specified role (default: admin).\n" +
		"  Username and password are prompted if not provided via flags.\n" +
		"  Use --password to skip the interactive prompt (e.g. in scripts).") {
		return nil
	}

	if err := fp.String("--username", &username); err != nil {
		return err
	}
	if err := fp.String("--password", &password); err != nil {
		return err
	}
	if err := fp.String("--role", &role); err != nil {
		return err
	}

	if username == "" {
		scanner := bufio.NewScanner(os.Stdin)
		fmt.Print("Username: ")
		scanner.Scan()
		username = strings.TrimSpace(scanner.Text())
		if username == "" {
			return fmt.Errorf("username cannot be empty")
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("read username: %w", err)
		}
	}

	if password == "" {
		fmt.Print("Password: ")
		pw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return fmt.Errorf("read password (use --password in non-interactive mode): %w", err)
		}
		password = string(pw)

		fmt.Print("Confirm password: ")
		confirm, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return fmt.Errorf("confirm password: %w", err)
		}
		if password != string(confirm) {
			return fmt.Errorf("passwords do not match")
		}
	}

	if role == "" {
		role = "admin"
	} else if !auth.ValidRole(role) {
		return fmt.Errorf("invalid role %q: must be admin, editor, or viewer", role)
	}

	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	userSvc := service.NewUser(client.Queries)
	if _, err := userSvc.Create(context.Background(), username, password, role); err != nil {
		if errs.KindOf(err) == errs.KindConflict {
			return fmt.Errorf("user '%s' already exists", username)
		}
		return fmt.Errorf("create user: %w", err)
	}

	fmt.Printf("user '%s' created with role '%s'\n", username, role)
	return nil
}
