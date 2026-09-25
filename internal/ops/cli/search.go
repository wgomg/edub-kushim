package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
)

func searchHandler(c *container.Container, args []string) error {
	p := flags.NewFlagParser(args)

	if p.Help("Usage: kushim search [--limit N] [--offset N] [--rebuild-index] <query>\n" +
		"  Full-text search across documents.\n\n" +
		"  --limit N          max results (default 20, max 100)\n" +
		"  --offset N         result offset (default 0)\n" +
		"  --rebuild-index    reindex tsvector GIN index") {
		return nil
	}

	limit := 20
	offset := 0
	rebuild := false

	if err := p.Int("--limit", &limit, 1, 100); err != nil {
		return err
	}
	if err := p.Int("--offset", &offset, 0, 1<<31); err != nil {
		return err
	}
	if err := p.Bool("--rebuild-index", &rebuild); err != nil {
		return err
	}
	args = p.Rest()

	if rebuild {
		return rebuildIndex(c)
	}

	if len(args) < 1 {
		return fmt.Errorf("usage: kushim search [--limit N] [--offset N] [--rebuild-index] <query>")
	}

	query := strings.Join(args, " ")

	engine, err := c.GetSearchEngine()
	if err != nil {
		return fmt.Errorf("failed to get search engine: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	results, err := engine.Search(ctx, query, int32(limit), int32(offset))
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(results) == 0 {
		c.GetLogger().Info("", "no results for: %s", query)
		return nil
	}

	c.GetLogger().Info("", "%d results for: %s", len(results), query)

	for _, r := range results {
		fmt.Printf("\n─── #%s ─────────────────────────────────────────────\n", r.DocumentID)
		fmt.Printf("  %s\n", r.Title)
		snippet := r.Snippet
		if snippet == "" {
			snippet = "(no snippet)"
		}
		fmt.Printf("  %s\n", highlightSnippet(snippet))
		fmt.Printf("  rank=%.4f  |  %s  |  %s\n",
			r.Rank,
			log.FormatBytes(uint64(r.FileSize)),
			r.CreatedAt.Format("2006-01-02"),
		)
	}

	return nil
}

func highlightSnippet(s string) string {
	s = strings.ReplaceAll(s, "<b>", "\033[1;33m")
	s = strings.ReplaceAll(s, "</b>", "\033[0m")
	return s
}

func rebuildIndex(c *container.Container) error {
	engine, err := c.GetSearchEngine()
	if err != nil {
		return fmt.Errorf("failed to get search engine: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	if err := engine.Reindex(ctx); err != nil {
		return err
	}

	c.GetLogger().Info("", "tsvector index rebuilt in %s", time.Since(start).Round(time.Millisecond))
	return nil
}
