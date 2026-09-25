// Package textreducer reduces document text to a target word count with
// TextRank so it fits model context budgets: the LLM prompts and the
// matcher's embedding inputs consume its output, which is why it stays a
// tools adapter rather than belonging to either domain.
package textreducer

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// TextReducer compresses content toward a target word count, keeping the
// most salient sentences.
type TextReducer interface {
	// Reduce returns content reduced toward targetWordCount words, scoring
	// word chunks of chunkSize tokens with the configured algorithm. The
	// context may cancel the ranking work.
	Reduce(ctx context.Context, content string, chunkSize, targetWordCount int) (*string, error)
	// Name reports the reducer implementation's identity.
	Name() string
}

// NewTextReducer builds the reducer selected by cfg.Command; every current
// value resolves to the TextRank implementation.
func NewTextReducer(logger *log.Logger, cfg config.ToolConfig) (TextReducer, error) {
	switch cfg.Command {
	case config.TextReducer.TextRank:
		tr, err := NewTextRank(logger, cfg)
		return tr, err
	default:
		return NewTextRank(logger, cfg)
	}
}
