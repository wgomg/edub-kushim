package pidfile

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestPath(t *testing.T) {
	got := Path("test-batch")
	want := filepath.Join(os.TempDir(), "kushim_test-batch.pid")
	if got != want {
		t.Errorf("Path() = %s; want %s", got, want)
	}
}

func TestRead_NotExist(t *testing.T) {
	pid, err := Read("nonexistent-batch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pid != 0 {
		t.Errorf("expected 0, got %d", pid)
	}
}

func TestRead_Valid(t *testing.T) {
	batchID := "read-valid"
	path := Path(batchID)
	if err := os.WriteFile(path, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	pid, err := Read(batchID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pid != 12345 {
		t.Errorf("expected 12345, got %d", pid)
	}
}

func TestRead_InvalidContent(t *testing.T) {
	batchID := "read-invalid"
	path := Path(batchID)
	if err := os.WriteFile(path, []byte("not-a-number"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	pid, err := Read(batchID)
	if err == nil {
		t.Fatal("expected error")
	}
	if pid != 0 {
		t.Errorf("expected 0, got %d", pid)
	}
}

func TestIsAlive_NotExist(t *testing.T) {
	alive := IsAlive("nonexistent-batch")
	if alive {
		t.Error("expected false")
	}
}

func TestAcquire_And_Release(t *testing.T) {
	batchID := "acquire-release"
	l, err := Acquire(batchID, false, func() {})
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	if _, err := os.Stat(Path(batchID)); os.IsNotExist(err) {
		t.Error("pid file should exist after Acquire")
	}

	l.Release()

	if _, err := os.Stat(Path(batchID)); !os.IsNotExist(err) {
		t.Error("pid file should be removed after Release")
	}
}

func TestAcquire_Force(t *testing.T) {
	batchID := "acquire-force"
	path := Path(batchID)
	if err := os.WriteFile(path, []byte("999999"), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := Acquire(batchID, true, func() {})
	if err != nil {
		t.Fatalf("Acquire with force=true failed: %v", err)
	}
	defer l.Release()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if pid != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), pid)
	}
}

func TestAcquire_AlreadyRunning(t *testing.T) {
	batchID := "acquire-running"
	l, err := Acquire(batchID, false, func() {})
	if err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}
	defer l.Release()

	_, err = Acquire(batchID, false, func() {})
	if err == nil {
		t.Error("expected error on second Acquire")
	}
}

func TestRelease_Idempotent(t *testing.T) {
	batchID := "release-idempotent"
	l, err := Acquire(batchID, false, func() {})
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	l.Release()
	l.Release()

	if _, err := os.Stat(Path(batchID)); !os.IsNotExist(err) {
		t.Error("pid file should not exist after Release")
	}
}

func TestAcquire_CallsOnSignal(t *testing.T) {
	batchID := "acquire-signal"
	var mu sync.Mutex
	called := false
	l, err := Acquire(batchID, false, func() {
		mu.Lock()
		called = true
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		l.Release()
		t.Fatalf("FindProcess: %v", err)
	}
	p.Signal(syscall.SIGTERM)

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	wasCalled := called
	mu.Unlock()

	if !wasCalled {
		t.Error("expected onSignal callback to be called on SIGTERM")
	}

	l.Release()
}
