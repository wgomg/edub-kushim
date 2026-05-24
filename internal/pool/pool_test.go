package pool

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/utils"
)

type mockRunner struct {
	mu       sync.Mutex
	calls    int32
	err      error
	blockCh  chan struct{}
}

func (m *mockRunner) Next(ctx context.Context) error {
	atomic.AddInt32(&m.calls, 1)
	if m.blockCh != nil {
		<-m.blockCh
	}
	return m.err
}

func (m *mockRunner) callCount() int32 {
	return atomic.LoadInt32(&m.calls)
}

func TestNew(t *testing.T) {
	p := New(utils.NewDiscardLogger(), &mockRunner{}, 3, 100*time.Millisecond)
	if p == nil {
		t.Fatal("expected non-nil pool")
	}
	if p.workers != 3 {
		t.Errorf("workers = %d, want 3", p.workers)
	}
	if p.interval != 100*time.Millisecond {
		t.Errorf("interval = %v, want 100ms", p.interval)
	}
}

func TestStartStop(t *testing.T) {
	runner := &mockRunner{}
	p := New(utils.NewDiscardLogger(), runner, 2, 10*time.Millisecond)

	ctx := context.Background()
	p.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p.Stop(stopCtx)

	calls := runner.callCount()
	if calls < 2 {
		t.Errorf("expected at least 2 calls across workers, got %d", calls)
	}
}

func TestStop_TwiceIsSafe(t *testing.T) {
	runner := &mockRunner{}
	p := New(utils.NewDiscardLogger(), runner, 1, time.Hour)
	p.Start(context.Background())

	stopCtx := context.Background()
	p.Stop(stopCtx)
	p.Stop(stopCtx)
}

func TestStop_Timeout(t *testing.T) {
	runner := &mockRunner{blockCh: make(chan struct{})}
	p := New(utils.NewDiscardLogger(), runner, 1, 10*time.Millisecond)
	p.Start(context.Background())

	time.Sleep(20 * time.Millisecond)

	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	p.Stop(stopCtx)

	close(runner.blockCh)
}

func TestWorkerLoop_RespectsStopCh(t *testing.T) {
	runner := &mockRunner{}
	p := New(utils.NewDiscardLogger(), runner, 1, time.Hour)
	p.Start(context.Background())

	stopCtx := context.Background()
	p.Stop(stopCtx)

	callsBefore := runner.callCount()

	time.Sleep(20 * time.Millisecond)

	if runner.callCount() != callsBefore {
		t.Error("worker continued running after Stop")
	}
}

func TestWorkerLoop_ContextCancelled(t *testing.T) {
	runner := &mockRunner{}
	p := New(utils.NewDiscardLogger(), runner, 1, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	time.Sleep(30 * time.Millisecond)
	callsBefore := runner.callCount()

	cancel()
	time.Sleep(30 * time.Millisecond)

	if runner.callCount() != callsBefore {
		t.Error("worker continued running after context cancel")
	}
}

func TestWorkerLoop_RunnerError(t *testing.T) {
	runner := &mockRunner{err: context.DeadlineExceeded}
	p := New(utils.NewDiscardLogger(), runner, 1, 10*time.Millisecond)
	p.Start(context.Background())

	time.Sleep(30 * time.Millisecond)

	stopCtx := context.Background()
	p.Stop(stopCtx)

	if runner.callCount() < 1 {
		t.Error("expected at least one call despite runner errors")
	}
}
