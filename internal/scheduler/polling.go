package scheduler

import (
	"context"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/pool"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type PollingScheduler struct {
	getConfig  func() config.PollingConfig
	logger     *utils.Logger
	kushimPath string
	semaphore  *pool.Semaphore

	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running atomic.Bool
}

func NewPollingScheduler(getConfig func() config.PollingConfig, logger *utils.Logger, kushimPath string, semaphore *pool.Semaphore) *PollingScheduler {
	return &PollingScheduler{
		getConfig:  getConfig,
		logger:     logger,
		kushimPath: kushimPath,
		semaphore:  semaphore,
	}
}

func (p *PollingScheduler) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ctx != nil {
		return
	}

	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.wg.Add(1)
	go p.run()
}

func (p *PollingScheduler) Stop() {
	p.mu.Lock()
	if p.cancel == nil {
		p.mu.Unlock()
		return
	}
	cancel := p.cancel
	p.mu.Unlock()

	cancel()
	p.wg.Wait()
}

func (p *PollingScheduler) run() {
	defer p.wg.Done()
	for {
		cfg := p.getConfig()
		interval := max(time.Duration(cfg.Interval)*time.Minute, time.Minute)
		if cfg.Enabled && config.IsWithinActiveWindows(cfg.Windows) {
			p.poll()
		} else if cfg.Enabled {
			p.logger.Debug(nil, "polling: disabled — outside active windows")
		} else {
			p.logger.Debug(nil, "polling: disabled")
		}
		select {
		case <-p.ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (p *PollingScheduler) poll() {
	if !p.running.CompareAndSwap(false, true) {
		p.logger.Debug(nil, "polling: skipped — previous poll still running")
		return
	}
	defer p.running.Store(false)

	if p.kushimPath == "" {
		p.logger.Error(nil, "polling: kushim binary not available")
		return
	}

	if !p.semaphore.Acquire() {
		p.logger.Debug(nil, "polling: skipped — max concurrent batches reached")
		return
	}

	p.logger.Debug(nil, "polling: forking kushim consume --bg")
	cmd := exec.Command(p.kushimPath, "consume", "--bg")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		p.logger.Error(nil, "polling: fork kushim consume --bg: %v", err)
		p.semaphore.Release()
		return
	}
	p.logger.Info(nil, "polling: worker forked (pid %d)", cmd.Process.Pid)

	go func() {
		if err := cmd.Wait(); err != nil {
			p.logger.Error(nil, "polling: worker pid %d exited: %v", cmd.Process.Pid, err)
		}
		p.semaphore.Release()
	}()
}
