package backup

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

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/version"
)

// StateFileName is the mirror state file's name at the destination root.
const StateFileName = ".edub-mirror.json"

// State is what a destination's mirror state file records.
type State struct {
	Mode      string `json:"mode"`
	Timestamp string `json:"timestamp"`
	Files     int64  `json:"files"`
	Bytes     int64  `json:"bytes"`
	Version   string `json:"version"`
}

// Result reports one mirror run: totals on the target, what rsync
// transferred, and the created, deleted and updated entries.
type Result struct {
	Files       int64 `json:"files"`
	Bytes       int64 `json:"bytes"`
	Transferred int64 `json:"transferred"`
	Created     int64 `json:"created"`
	Deleted     int64 `json:"deleted"`
	Updated     int64 `json:"updated"`
}

// Available reports whether rsync is on PATH.
func Available() bool {
	_, err := exec.LookPath("rsync")
	return err == nil
}

// IsRemoteTarget reports whether the destination is remote
// (host:path form).
func IsRemoteTarget(dest string) bool {
	return config.IsRemoteMirrorTarget(dest)
}

// ValidateDestination refuses mirror destinations that would recurse
// into the data, as the config's mirror validation does.
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

// Run mirrors the storage directory to the destination, mirroring
// deletions, and writes the state file at the destination. The returned
// stats are parsed from rsync's stats output.
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
			res.Files = parseRegCount(line)
		case strings.HasPrefix(line, "Number of regular files transferred:"):
			res.Transferred = parseCount(strings.TrimPrefix(line, "Number of regular files transferred:"))
		case strings.HasPrefix(line, "Number of created files:"):
			res.Created = parseRegCount(line)
		case strings.HasPrefix(line, "Number of deleted files:"):
			res.Deleted = parseLeadingCount(strings.TrimPrefix(line, "Number of deleted files:"))
		case strings.HasPrefix(line, "Total file size:"):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "Total file size:"))
			if before, _, ok := strings.Cut(rest, " bytes"); ok {
				res.Bytes = parseCount(before)
			}
		}
	}
	res.Updated = max(res.Transferred-res.Created, 0)
	return &res
}

func parseRegCount(line string) int64 {
	_, after, ok := strings.Cut(line, "(reg:")
	if !ok {
		return 0
	}
	rest := strings.TrimSpace(after)
	end := 0
	for end < len(rest) && (rest[end] == ',' || rest[end] >= '0' && rest[end] <= '9') {
		end++
	}
	return parseCount(rest[:end])
}

func parseLeadingCount(rest string) int64 {
	rest = strings.TrimSpace(rest)
	if i := strings.IndexAny(rest, " ("); i >= 0 {
		rest = rest[:i]
	}
	return parseCount(rest)
}

func parseCount(s string) int64 {
	n, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(s), ",", ""), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func formatCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	start := 0
	if s[0] == '-' {
		start = 1
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		if i > start && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// Summary renders the result as one human-readable line.
func (r *Result) Summary() string {
	return fmt.Sprintf("%s files (%d transferred: %d created, %d updated, %d deleted), %s bytes",
		formatCount(r.Files), r.Transferred, r.Created, r.Updated, r.Deleted, formatCount(r.Bytes))
}

// WriteState records a successful run in the destination's state file.
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

// RunLocked acquires the maintenance lock (skipping cleanly when busy),
// mirrors, and records the mirror task's completion; onWait reports the
// queued count while waiting for the lock. Returns the result and the
// lock token, with ok=false when the lock was busy.
func RunLocked(ctx context.Context, queries *database.Queries, logger *log.Logger, storageDir, dest string, onWait ...func(count int64)) (*Result, string, error) {
	var wait func(count int64)
	if len(onWait) > 0 {
		wait = onWait[0]
	}
	return runLocked(ctx, queries, logger, storageDir, dest, wait, nil, uuid.NullUUID{})
}

// RunLockedWithProgress is RunLocked with progress callbacks: onWait for
// the lock wait, onSync when the sync starts. token, when valid, is the
// blocking task's claim token the run settles.
func RunLockedWithProgress(ctx context.Context, queries *database.Queries, logger *log.Logger, storageDir, dest string, onWait func(count int64), onSync func(), token uuid.NullUUID) (*Result, string, error) {
	return runLocked(ctx, queries, logger, storageDir, dest, onWait, onSync, token)
}

func runLocked(ctx context.Context, queries *database.Queries, logger *log.Logger, storageDir, dest string, onWait func(count int64), onSync func(), token uuid.NullUUID) (*Result, string, error) {
	stopHeartbeat := database.StartMaintenanceLockHeartbeat(ctx, queries, logger, 5*time.Minute, token)
	defer stopHeartbeat()

	if err := database.WaitForTaskDrain(ctx, queries, logger, "mirror", onWait); err != nil {
		return nil, "", err
	}

	if onSync != nil {
		onSync()
	}
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

	logger.Info(nil, "mirror completed: %s", result.Summary())
	return result, state.Timestamp, nil
}
