package tagmatcher

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TagMatcher interface {
	Match(ctx context.Context, input string, tagsToMatch map[string][]float32) ([]string, error)
	MatchEach(ctx context.Context, queries []string, tagsToMatch map[string][]float32) ([]string, error)
	Close()
	Name() string
}

func NewTagMatcher(logger *utils.Logger, tmConfig config.TagMatcherConfig) (TagMatcher, error) {
	return NewHugot(logger, tmConfig, "tagmatcher")
}
