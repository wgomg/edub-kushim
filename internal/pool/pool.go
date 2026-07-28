package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wgomg/edub-kushim/internal/utils"
)

type Runner interface {
	Next(ctx context.Context, taskType string) error
}

type Pool struct {
	logger   *utils.Logger
	runner   Runner
	workers  int
	interval time.Duration
	taskType string
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc
}

func New(logger *utils.Logger, runner Runner, workers int, interval time.Duration, taskType string) *Pool {
	return &Pool{
		logger:   logger,
		runner:   runner,
		workers:  workers,
		interval: interval,
		taskType: taskType,
		stopCh:   make(chan struct{}),
	}
}

func (p *Pool) Start(ctx context.Context) {
	p.ctx, p.cancel = context.WithCancel(ctx)
	for i := range p.workers {
		p.wg.Add(1)
		go p.workerLoop(i)
		p.logger.Info(nil, "worker %d started", i)
	}
}

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
			mem := utils.ReadMemFull()
			p.logger.Debug(nil, "%s periodic memory: %s", logPrefix, utils.FormatMemFull(mem))
		case <-time.After(p.interval):
			if err := p.runner.Next(p.ctx, p.taskType); err != nil {
				p.logger.Error(nil, "%s: %v", logPrefix, err)
			}
		}
	}
}
