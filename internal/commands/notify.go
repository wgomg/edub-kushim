package commands

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wgomg/edub-kushim/internal/utils"
)

func listenForBatchNotifications(ctx context.Context, dsn string, notifyCh chan<- struct{}, logger *utils.Logger) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		logger.Error(nil, "notify: parse DSN: %v", err)
		return
	}
	poolCfg.MinConns = 1
	poolCfg.MaxConns = 1
	poolCfg.MaxConnLifetime = 1 * time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		logger.Error(nil, "notify: create pool: %v", err)
		return
	}
	defer pool.Close()

	for {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Error(nil, "notify: acquire connection: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		if _, err := conn.Exec(ctx, "LISTEN batch_queued"); err != nil {
			logger.Error(nil, "notify: LISTEN: %v", err)
			conn.Release()
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		logger.Info(nil, "notify: listening for batch_queued notifications")

		for {
			_, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				if ctx.Err() != nil {
					conn.Release()
					return
				}
				logger.Error(nil, "notify: WaitForNotification: %v", err)
				conn.Release()
				break
			}

			select {
			case notifyCh <- struct{}{}:
			default:
			}
		}
	}
}
