package pool

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wgomg/edub-kushim/internal/utils"
)

type countRunner struct {
	count atomic.Int32
}

func (r *countRunner) Next(_ context.Context, _ string) error {
	r.count.Add(1)
	return nil
}

func TestPool_StartStop(t *testing.T) {
	runner := &countRunner{}
	p := New(utils.NewDiscardLogger(), runner, 2, 10*time.Millisecond, "test")

	ctx := t.Context()
	p.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	p.Stop(stopCtx)

	if n := runner.count.Load(); n == 0 {
		t.Error("expected runner to be called at least once")
	}
}

func TestPool_ContextCancellation(t *testing.T) {
	runner := &countRunner{}
	p := New(utils.NewDiscardLogger(), runner, 1, 10*time.Millisecond, "test")

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	time.Sleep(30 * time.Millisecond)
	cancel()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	p.Stop(stopCtx)

	if n := runner.count.Load(); n == 0 {
		t.Error("expected runner to be called at least once before cancellation")
	}
}

func TestPool_DoubleStop(t *testing.T) {
	runner := &countRunner{}
	p := New(utils.NewDiscardLogger(), runner, 1, 50*time.Millisecond, "test")

	ctx := context.Background()
	p.Start(ctx)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	p.Stop(stopCtx)
	p.Stop(stopCtx)
}
