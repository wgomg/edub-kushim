package tagmatcher

import (
	"context"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

// right now there's only one engine adapter for tag matching so it doesn't matter to have the
// interface with same type signatures as concrete hugot implementation, if there's ever another
// engine supported then these should be abstracted... somehow
type TagMatcher interface {
	Match(ctx context.Context, docId, input string, tagsToMatch map[string][]float32) ([]string, error)
	MatchEach(ctx context.Context, docId string, queries []string, tagsToMatch map[string][]float32) ([]string, error)
	Encode(ctx context.Context, docId *string, texts []string) ([][]float32, error)
	Close()
	Name() string
}

func NewTagMatcher(logger *utils.Logger, tmConfig config.TagMatcherConfig) (TagMatcher, error) {
	return NewHugot(logger, tmConfig, "tagmatcher")
}
