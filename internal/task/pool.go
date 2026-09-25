package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/types"
)

// Nexter is the claim loop a Pool drives: one attempt to claim and run the
// next task of the given type. Runner.Next is the production Nexter; the
// seam exists so tests and other drivers can replace the loop.
type Nexter interface {
	Next(ctx context.Context, taskType types.TaskType) error
}

// Pool runs worker goroutines that poll a Nexter on a fixed interval for a
// single task type. Workers restart after a panic or an error, periodic
// memory snapshots go to the log at debug level, and Stop drains them up
// to the caller's context deadline.
type Pool struct {
	logger   *log.Logger
	runner   Nexter
	workers  int
	interval time.Duration
	taskType string
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewPool prepares a pool for one task type; Start launches it. workers
// is the fixed worker count and interval the claim cadence.
func NewPool(logger *log.Logger, runner Nexter, workers int, interval time.Duration, taskType string) *Pool {
	return &Pool{
		logger:   logger,
		runner:   runner,
		workers:  workers,
		interval: interval,
		taskType: taskType,
		stopCh:   make(chan struct{}),
	}
}

// Start launches the workers. Call it once; a second call replaces the
// context the first batch of workers polls with.
func (p *Pool) Start(ctx context.Context) {
	p.ctx, p.cancel = context.WithCancel(ctx)
	for i := range p.workers {
		p.wg.Add(1)
		go p.workerLoop(i)
		p.logger.Info(nil, "worker %d started", i)
	}
}

// Stop cancels the workers and waits for them, giving up after the
// caller's context deadline. Idempotent.
func (p *Pool) Stop(ctx context.Context) {
	p.stopOnce.Do(func() {
		close(p.stopCh)
		if p.cancel != nil {
			p.cancel()
		}
	})

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		p.logger.Info(nil, "all workers stopped")
	case <-ctx.Done():
		p.logger.Info(nil, "worker shutdown timed out")
	}
}

func (p *Pool) workerLoop(id int) {
	defer p.wg.Done()
	logPrefix := fmt.Sprintf("[%s worker %d]", p.taskType, id)

	for {
		err := p.runWorker(id, logPrefix)
		if err == nil {
			return
		}
		p.logger.Error(nil, "%s restarting after error: %v", logPrefix, err)
		select {
		case <-p.stopCh:
			return
		case <-p.ctx.Done():
			return
		case <-time.After(p.interval):
		}
	}
}

func (p *Pool) runWorker(id int, logPrefix string) (rerr error) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error(nil, "%s panic recovered: %v", logPrefix, r)
			rerr = fmt.Errorf("worker panic: %v", r)
		}
	}()

	memTicker := time.NewTicker(5 * time.Minute)
	defer memTicker.Stop()

	for {
		select {
		case <-p.stopCh:
			p.logger.Info(nil, "%s stopping", logPrefix)
			return nil
		case <-p.ctx.Done():
			return nil
		case <-memTicker.C:
			mem := log.ReadMemFull()
			p.logger.Debug(nil, "%s periodic memory: %s", logPrefix, log.FormatMemFull(mem))
		case <-time.After(p.interval):
			if err := p.runner.Next(p.ctx, types.TaskType(p.taskType)); err != nil {
				p.logger.Error(nil, "%s: %v", logPrefix, err)
			}
		}
	}
}
