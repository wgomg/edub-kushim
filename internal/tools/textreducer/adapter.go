package textreducer

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

type TextReducer interface {
	Reduce(ctx context.Context, content string, chunkSize, targetWordCount int) (*string, error)
	Name() string
}

func NewTextReducer(logger *log.Logger, cfg config.ToolConfig) (TextReducer, error) {
	switch cfg.Command {
	case config.TextReducer.TextRank:
		tr, err := NewTextRank(logger, cfg)
		return tr, err
	default:
		return NewTextRank(logger, cfg)
	}
}
