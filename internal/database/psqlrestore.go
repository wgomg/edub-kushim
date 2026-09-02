package database

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

func CheckRestoreTooling(runtime, container string) error {
	switch runtime {
	case "", "host":
		if _, err := exec.LookPath("psql"); err != nil {
			return fmt.Errorf("psql not found on PATH — install postgresql-client, or set database.runtime to docker/podman with database.container")
		}
	case "docker", "podman":
		if container == "" {
			return fmt.Errorf("database.container is required when database.runtime is %s", runtime)
		}
		if _, err := exec.LookPath(runtime); err != nil {
			return fmt.Errorf("%s not found on PATH — install it, or set database.runtime to host with psql installed", runtime)
		}
	case "remote":
		return fmt.Errorf("database.runtime=remote is not supported for automated restore — unzip the archive and run psql manually on the remote host")
	default:
		return fmt.Errorf("database.runtime must be one of host, docker, podman, remote, got %q", runtime)
	}
	return nil
}

func RestoreDumpViaPSQL(ctx context.Context, runtime, container, dsn, dumpPath string) error {
	if err := CheckRestoreTooling(runtime, container); err != nil {
		return err
	}

	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("parse connection dsn: %w", err)
	}
	password, _ := u.User.Password()

	var cmd *exec.Cmd
	switch runtime {
	case "", "host":
		if u.User != nil {
			u.User = url.User(u.User.Username())
		}
		cmd = exec.CommandContext(ctx, "psql", "-X", "-v", "ON_ERROR_STOP=1", "-f", dumpPath, u.String())
		if password != "" {
			cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
		}
	case "docker", "podman":
		user := ""
		if u.User != nil {
			user = u.User.Username()
		}
		args := []string{"exec", "-i"}
		if password != "" {
			args = append(args, "-e", "PGPASSWORD="+password)
		}
		args = append(args, container,
			"psql", "-X", "-v", "ON_ERROR_STOP=1",
			"-h", "localhost", "-U", user, "-d", strings.TrimPrefix(u.Path, "/"))
		cmd = exec.CommandContext(ctx, runtime, args...)
		f, err := os.Open(dumpPath)
		if err != nil {
			return fmt.Errorf("open dump file: %w", err)
		}
		defer f.Close()
		cmd.Stdin = f
	case "remote":
		return fmt.Errorf("database.runtime=remote is not supported for automated restore — unzip the archive and run psql manually on the remote host")
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("restore failed: %s", msg)
	}
	return nil
}
