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

func TestJaccardIDs(t *testing.T) {
	tests := []struct {
		name string
		a, b []uint32
		want float64
	}{
		{name: "both empty", a: nil, b: nil, want: 0},
		{name: "left empty", a: nil, b: []uint32{1, 2}, want: 0},
		{name: "right empty", a: []uint32{1, 2}, b: nil, want: 0},
		{name: "disjoint", a: []uint32{1, 2}, b: []uint32{3, 4}, want: 0},
		{name: "partial overlap", a: []uint32{1, 2, 3}, b: []uint32{2, 3, 4}, want: 2.0 / 4.0},
		{name: "identical", a: []uint32{1, 2, 3}, b: []uint32{1, 2, 3}, want: 1},
		{name: "single match", a: []uint32{5}, b: []uint32{5}, want: 1},
		{name: "subset", a: []uint32{1}, b: []uint32{1, 2, 3}, want: 1.0 / 3.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jaccardIDs(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("jaccardIDs(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// vocabularyWords renders n words drawn from a small rotating vocabulary so successive chunks share
// tokens.
func vocabularyWords(n int) string {
	parts := make([]string, n)
	for i := range n {
		parts[i] = fmt.Sprintf("term%d", i%200)
	}
	return strings.Join(parts, " ")
}

// referenceJaccard recomputes Jaccard from unique-token maps, the pre-refactor set semantics the new
// merge must reproduce exactly.
func referenceJaccard(a, b map[string]int) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	intersection := 0
	for token := range a {
		if _, ok := b[token]; ok {
			intersection++
		}
	}
	return float64(intersection) / float64(len(a)+len(b)-intersection)
}

func TestCreateChunks_UniqueIDInvariants(t *testing.T) {
	content := vocabularyWords(1200) + " " + numberedSentences(300)
	chunks := createChunks(strings.Fields(cleanUp(content)), ReduceChunkSize)
	if len(chunks) < 2 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}

	for _, chunk := range chunks {
		if len(chunk.UniqueIDs) != len(chunk.TokenFrequencies) {
			t.Errorf("chunk %d: %d unique IDs for %d unique tokens",
				chunk.Id, len(chunk.UniqueIDs), len(chunk.TokenFrequencies))
		}
		for i := 1; i < len(chunk.UniqueIDs); i++ {
			if chunk.UniqueIDs[i] <= chunk.UniqueIDs[i-1] {
				t.Errorf("chunk %d: IDs not strictly ascending: %v", chunk.Id, chunk.UniqueIDs)
				break
			}
		}
	}
}

func TestJaccardIDSEqualsSetJaccard(t *testing.T) {
	corpora := map[string]string{
		"overlapping vocabulary": vocabularyWords(1200),
		"punctuated sentences":   numberedSentences(300),
		"run-on words":           numberedWords(1200),
	}

	for name, content := range corpora {
		t.Run(name, func(t *testing.T) {
			chunks := createChunks(strings.Fields(cleanUp(content)), ReduceChunkSize)
			for i := range chunks {
				for j := i + 1; j < len(chunks); j++ {
					want := referenceJaccard(chunks[i].TokenFrequencies, chunks[j].TokenFrequencies)
					got := jaccardIDs(chunks[i].UniqueIDs, chunks[j].UniqueIDs)
					if got != want {
						t.Fatalf("chunks %d,%d: jaccardIDs = %v, reference = %v", i, j, got, want)
					}
				}
			}
		})
	}
}
