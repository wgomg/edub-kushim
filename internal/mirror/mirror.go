package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/utils"
	"github.com/wgomg/edub-kushim/internal/version"
)

const StateFileName = ".edub-mirror.json"

type State struct {
	Mode      string `json:"mode"`
	Timestamp string `json:"timestamp"`
	Files     int64  `json:"files"`
	Bytes     int64  `json:"bytes"`
	Version   string `json:"version"`
}

type Result struct {
	Files int64 `json:"files"`
	Bytes int64 `json:"bytes"`
}

func Available() bool {
	_, err := exec.LookPath("rsync")
	return err == nil
}

func IsRemoteTarget(dest string) bool {
	return config.IsRemoteMirrorTarget(dest)
}

func ValidateDestination(dest, storageDir, backupPath string) error {
	return config.ValidateMirrorDestination(dest, storageDir, backupPath)
}

func rsyncCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "rsync", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	return cmd
}

func Run(ctx context.Context, storageDir, dest string) (*Result, error) {
	if !Available() {
		return nil, fmt.Errorf("rsync is not installed")
	}
	cmd := rsyncCommand(ctx, "-a", "--delete", "--info=stats2", "--timeout=600", "--", storageDir+"/", dest)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("rsync failed: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}
	return parseStats(stdout.String() + stderr.String()), nil
}

func parseStats(output string) *Result {
	var res Result
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Number of files:"):
			if i := strings.Index(line, "(reg:"); i >= 0 {
				rest := line[i+len("(reg:"):]
				if before, _, ok := strings.Cut(rest, ","); ok {
					res.Files = parseCount(before)
				}
			}
		case strings.HasPrefix(line, "Total file size:"):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "Total file size:"))
			if before, _, ok := strings.Cut(rest, " bytes"); ok {
				res.Bytes = parseCount(before)
			}
		}
	}
	return &res
}

func parseCount(s string) int64 {
	n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(s), ",", ""), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func WriteState(dest string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mirror state: %w", err)
	}
	if IsRemoteTarget(dest) {
		tmp, err := os.CreateTemp("", ".edub-mirror-*.json")
		if err != nil {
			return fmt.Errorf("create temp state file: %w", err)
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			return fmt.Errorf("write temp state file: %w", err)
		}
		tmp.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := rsyncCommand(ctx, "-a", "--", tmp.Name(), dest+"/"+StateFileName)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("upload mirror state: %w\n%s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := os.WriteFile(filepath.Join(dest, StateFileName), data, 0644); err != nil {
		return fmt.Errorf("write mirror state: %w", err)
	}
	return nil
}

func RunLocked(ctx context.Context, queries *database.Queries, logger *utils.Logger, storageDir, dest string) (*Result, string, error) {
	if err := database.WaitForTaskDrain(ctx, queries, logger, "mirror"); err != nil {
		return nil, "", err
	}

	stopHeartbeat := StartHeartbeat(ctx, queries, logger, 5*time.Minute)
	defer stopHeartbeat()

	logger.Info(nil, "starting mirror to %s", dest)
	result, err := Run(ctx, storageDir, dest)
	if err != nil {
		return nil, "", fmt.Errorf("mirror failed: %w", err)
	}

	state := State{
		Mode:      "mirror",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Files:     result.Files,
		Bytes:     result.Bytes,
		Version:   version.Version,
	}
	if err := WriteState(dest, state); err != nil {
		logger.Error(nil, "write mirror state: %v", err)
	}

	logger.Info(nil, "mirror completed: %d files, %d bytes", result.Files, result.Bytes)
	return result, state.Timestamp, nil
}

func StartHeartbeat(ctx context.Context, queries *database.Queries, logger *utils.Logger, interval time.Duration) func() {
	stopCh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				if _, err := queries.TouchBackupLock(ctx); err != nil {
					logger.Error(nil, "mirror heartbeat: touch backup lock: %v", err)
				}
			}
		}
	}()
	return func() { close(stopCh) }
}
