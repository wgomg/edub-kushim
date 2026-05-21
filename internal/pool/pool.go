package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wgomg/edub-kushim/internal/utils"
)

type Runner interface {
	Next(ctx context.Context) error
}

type Pool struct {
	logger   *utils.Logger
	runner   Runner
	workers  int
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func New(logger *utils.Logger, runner Runner, workers int, interval time.Duration) *Pool {
	return &Pool{
		logger:   logger,
		runner:   runner,
		workers:  workers,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

func (p *Pool) Start() {
	for i := range p.workers {
		p.wg.Add(1)
		go p.workerLoop(i)
		p.logger.Info(nil, "worker %d started", i)
	}
}

func (p *Pool) Stop(ctx context.Context) {
	p.stopOnce.Do(func() {
		close(p.stopCh)
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
	logPrefix := fmt.Sprintf("[worker %d]", id)

	for {
		select {
		case <-p.stopCh:
			p.logger.Info(nil, "%s stopping", logPrefix)
			return
		case <-time.After(p.interval):
			if err := p.runner.Next(context.Background()); err != nil {
				p.logger.Error(nil, "%s: %v", logPrefix, err)
			}
		}
	}
}
