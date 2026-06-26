package commands

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func searchHandler(c *Container, args []string) error {
	p := NewFlagParser(args)

	if p.Help("Usage: kushim search [--limit N] [--offset N] [--rebuild-index] <query>\n" +
		"  Full-text search across documents.\n\n" +
		"  --limit N          max results (default 20, max 100)\n" +
		"  --offset N         result offset (default 0)\n" +
		"  --rebuild-index    rebuild FTS5 index from document table") {
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
		c.logger.Info(nil, "no results for: %s", query)
		return nil
	}

	c.logger.Info(nil, "%d results for: %s", len(results), query)

	for _, r := range results {
		fmt.Printf("\n─── #%d ─────────────────────────────────────────────\n", r.DocumentID)
		fmt.Printf("  %s\n", r.Title)
		snippet := r.Snippet
		if snippet == "" {
			snippet = "(no snippet)"
		}
		fmt.Printf("  %s\n", highlightSnippet(snippet))
		fmt.Printf("  rank=%.4f  |  %s  |  %s\n",
			r.Rank,
			formatSize(r.FileSize),
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

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func rebuildIndex(c *Container) error {
	client, err := c.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get database: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	if err := client.RebuildDocumentFTS(ctx); err != nil {
		return fmt.Errorf("rebuild failed: %w", err)
	}

	c.logger.Info(nil, "FTS index rebuilt in %s", time.Since(start).Round(time.Millisecond))
	return nil
}
