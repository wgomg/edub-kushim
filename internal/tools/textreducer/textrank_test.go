package textreducer

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// newTestTextRank builds the reducer with the production chunk window so the bounds mirror real calls.
func newTestTextRank(t *testing.T) *TextRank {
	t.Helper()
	tr, err := NewTextRank(log.NewDiscardLogger(), config.ToolConfig{})
	if err != nil {
		t.Fatalf("NewTextRank: %v", err)
	}
	return tr
}

// numberedWords renders n distinct words with no sentence punctuation, the shape of run-on
// extractions such as OCR'd lists and stripped tables.
func numberedWords(n int) string {
	parts := make([]string, n)
	for i := range n {
		parts[i] = fmt.Sprintf("item%d", i)
	}
	return strings.Join(parts, " ")
}

// numberedSentences renders n four-word sentences, each terminated by a period.
func numberedSentences(n int) string {
	var b strings.Builder
	for i := range n {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "item%d item%d item%d item%d.", i*4, i*4+1, i*4+2, i*4+3)
	}
	return b.String()
}

// numberedParagraphs renders n four-word lines with no sentence punctuation, separated by blank
// lines.
func numberedParagraphs(n int) string {
	var b strings.Builder
	for i := range n {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "item%d item%d item%d item%d", i*4, i*4+1, i*4+2, i*4+3)
	}
	return b.String()
}

// reduceWordCount runs Reduce and reports the result's word count, failing when the reducer errors.
func reduceWordCount(t *testing.T, tr *TextRank, content string, target int) int {
	t.Helper()
	got, err := tr.Reduce(context.Background(), content, ReduceChunkSize, target)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if got == nil {
		t.Fatal("Reduce returned a nil result")
	}
	return len(strings.Fields(*got))
}

// assertReducedTowardTarget checks the reduction contract: output below the input, and never past
// the target by more than two chunk windows, the most the selection loop can overshoot.
func assertReducedTowardTarget(t *testing.T, inputWords, outputWords, target int) {
	t.Helper()
	if outputWords >= inputWords {
		t.Errorf("no reduction: %d -> %d words (target %d)", inputWords, outputWords, target)
	}
	if limit := target + 2*ReduceChunkSize; outputWords > limit {
		t.Errorf("output %d words exceeds the bound %d for target %d", outputWords, limit, target)
	}
}

func TestReduce_PunctuationFreeContent(t *testing.T) {
	tr := newTestTextRank(t)
	const target = 2000
	content := numberedWords(3000)
	inputWords := len(strings.Fields(content))
	assertReducedTowardTarget(t, inputWords, reduceWordCount(t, tr, content, target), target)
}

func TestReduce_NewlineSeparatedContent(t *testing.T) {
	tr := newTestTextRank(t)
	const target = 2000
	content := numberedParagraphs(750)
	inputWords := len(strings.Fields(content))
	assertReducedTowardTarget(t, inputWords, reduceWordCount(t, tr, content, target), target)
}

func TestReduce_OversizedSentenceAmongSentences(t *testing.T) {
	tr := newTestTextRank(t)
	const target = 500
	content := numberedSentences(10) + " " + numberedWords(2000) + " " + numberedSentences(10)
	inputWords := len(strings.Fields(content))
	assertReducedTowardTarget(t, inputWords, reduceWordCount(t, tr, content, target), target)
}

func TestReduce_PunctuatedContentStillReduces(t *testing.T) {
	tr := newTestTextRank(t)
	const target = 2000
	content := numberedSentences(750)
	inputWords := len(strings.Fields(content))
	assertReducedTowardTarget(t, inputWords, reduceWordCount(t, tr, content, target), target)
}

func TestReduce_ContentAtOrBelowChunkWindowUnchanged(t *testing.T) {
	tr := newTestTextRank(t)

	short := "content that already fits the requested target"
	got, err := tr.Reduce(context.Background(), short, ReduceChunkSize, 2000)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if *got != short {
		t.Errorf("short content changed: got %q", *got)
	}

	empty, err := tr.Reduce(context.Background(), "", ReduceChunkSize, 2000)
	if err != nil {
		t.Fatalf("Reduce: %v", err)
	}
	if *empty != "" {
		t.Errorf("empty content changed: got %q", *empty)
	}
}
