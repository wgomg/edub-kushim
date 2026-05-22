package pidfile

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const prefix = "kushim_"

func Path(batchID string) string {
	return filepath.Join(os.TempDir(), prefix+batchID+".pid")
}

func Read(batchID string) (int, error) {
	data, err := os.ReadFile(Path(batchID))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read pid file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse pid file: %w", err)
	}
	return pid, nil
}

func IsAlive(batchID string) bool {
	alive, err := isAlive(Path(batchID))
	return alive && err == nil
}

type Lock struct {
	once sync.Once
	path string
	done chan struct{}
}

func Acquire(batchID string, force bool, onSignal func()) (*Lock, error) {
	path := Path(batchID)

	alive, err := isAlive(path)
	if err != nil {
		return nil, fmt.Errorf("check pid file %s: %w", path, err)
	}
	if alive && !force {
		data, _ := os.ReadFile(path)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return nil, fmt.Errorf("batch %s is already running (PID %d)", batchID, pid)
	}

	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, fmt.Errorf("write pid file: %w", err)
	}

	l := &Lock{path: path, done: make(chan struct{})}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sigCh:
			onSignal()
		case <-l.done:
		}
		signal.Stop(sigCh)
	}()

	return l, nil
}

func (l *Lock) Release() {
	l.once.Do(func() {
		close(l.done)
		os.Remove(l.path)
	})
}

func isAlive(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, nil
	}

	if err := syscall.Kill(pid, 0); err != nil {
		return false, nil
	}
	return true, nil
}
