package tools

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/documents/textextractor"
	"github.com/wgomg/edub-kushim/internal/log"
)

// TestRepairUTF8 pins the extraction repair contract that keeps PostgreSQL from rejecting text_content:
// CESU-8 surrogate pairs recombine into proper characters and every other invalid sequence becomes U+FFFD
// so stored text is always valid UTF-8.
func TestRepairUTF8(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		want         string
		wantPairs    int
		wantReplaced int
	}{
		{"clean ascii passes through identical", "plain document text", "plain document text", 0, 0},
		{"clean multibyte passes through identical", "漢字テスト \uFFFD ok", "漢字テスト \uFFFD ok", 0, 0},
		{"empty input stays empty", "", "", 0, 0},
		{"cesu8 pair recombines into one character", "\xed\xa0\xb5\xed\xb8\x9e", "\U0001d61e", 1, 0},
		{"pair embedded in prose keeps its surroundings", "caf\xc3\xa9 \xed\xa0\xb5\xed\xb8\x9e end", "caf\xc3\xa9 \U0001d61e end", 1, 0},
		{"lone high surrogate becomes one replacement", "\xed\xa0\xb5", "\uFFFD", 0, 1},
		{"lone low surrogate becomes one replacement", "\xed\xb8\x9e", "\uFFFD", 0, 1},
		{"two consecutive lone highs become two replacements", "\xed\xa0\xb4\xed\xa0\xb5", "\uFFFD\uFFFD", 0, 2},
		{"high surrogate without a following low keeps its tail", "ab\xed\xa0\xb5X", "ab\uFFFDX", 0, 1},
		{"ed with a bad continuation byte replaces per byte", "\xed\x28\x9e", "\uFFFD(\uFFFD", 0, 2},
		{"invalid lead byte replaces per byte", "ab\xff", "ab\uFFFD", 0, 1},
		{"truncated multibyte sequence replaces per byte", "ab\xf0\x9d", "ab\uFFFD\uFFFD", 0, 2},
		{"truncated pair repairs the high and replaces the rest", "x\xed\xa0\xb5\xed\xa0", "x\uFFFD\uFFFD\uFFFD", 0, 3},
		{"production failure shape repairs in place", "JqA524hA\xed\xa0\xb5tail", "JqA524hA\uFFFDtail", 0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, pairs, replaced := repairUTF8(tt.in)
			if got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if pairs != tt.wantPairs {
				t.Errorf("pairs = %d, want %d", pairs, tt.wantPairs)
			}
			if replaced != tt.wantReplaced {
				t.Errorf("replaced = %d, want %d", replaced, tt.wantReplaced)
			}
			if !utf8.ValidString(got) {
				t.Errorf("repaired text %q is still invalid UTF-8", got)
			}
		})
	}
}

// stubTextExtractor serves canned extraction output to the runner boundary.
type stubTextExtractor struct {
	text *string
	err  error
}

func (s *stubTextExtractor) Extract(context.Context, string, string) (*string, error) {
	return s.text, s.err
}

func (s *stubTextExtractor) CanHandle(string) bool { return true }

func (s *stubTextExtractor) Name() string { return "stub" }

var _ textextractor.TextExtractor = (*stubTextExtractor)(nil)

// TestExtractText_RepairsInvalidUTF8 pins the runner seam: dirty extractor output is stored repaired,
// while clean, nil, and failing extractors keep their existing behavior.
func TestExtractText_RepairsInvalidUTF8(t *testing.T) {
	t.Run("dirty extractor output is repaired before it reaches the database", func(t *testing.T) {
		dirty := "\xed\xa0\xb5 stored\xff text"
		r := &Runner{logger: log.NewDiscardLogger(), config: &config.Config{}, textExtractor: &stubTextExtractor{text: &dirty}}

		result, err := r.ExtractText(context.Background(), newTestPDF(t), "application/pdf")
		if err != nil {
			t.Fatalf("ExtractText: %v", err)
		}
		if result.Text == nil {
			t.Fatal("expected text, got nil")
		}
		if want := "\uFFFD stored\uFFFD text"; *result.Text != want {
			t.Errorf("text = %q, want %q", *result.Text, want)
		}
		if !utf8.ValidString(*result.Text) {
			t.Errorf("repaired text %q is still invalid UTF-8", *result.Text)
		}
	})

	t.Run("clean extractor output passes through unchanged", func(t *testing.T) {
		clean := "plain document text"
		r := &Runner{logger: log.NewDiscardLogger(), config: &config.Config{}, textExtractor: &stubTextExtractor{text: &clean}}

		result, err := r.ExtractText(context.Background(), newTestPDF(t), "application/pdf")
		if err != nil {
			t.Fatalf("ExtractText: %v", err)
		}
		if result.Text == nil || *result.Text != clean {
			t.Errorf("text = %v, want the input unchanged", result.Text)
		}
	})

	t.Run("nil extractor output stays nil", func(t *testing.T) {
		r := &Runner{logger: log.NewDiscardLogger(), config: &config.Config{}, textExtractor: &stubTextExtractor{}}

		result, err := r.ExtractText(context.Background(), newTestPDF(t), "application/pdf")
		if err != nil {
			t.Fatalf("ExtractText: %v", err)
		}
		if result.Text != nil {
			t.Errorf("text = %q, want nil", *result.Text)
		}
	})

	t.Run("extractor failure propagates before any repair", func(t *testing.T) {
		r := &Runner{logger: log.NewDiscardLogger(), config: &config.Config{}, textExtractor: &stubTextExtractor{err: context.DeadlineExceeded}}

		if _, err := r.ExtractText(context.Background(), newTestPDF(t), "application/pdf"); err == nil || !strings.Contains(err.Error(), "text extractor") {
			t.Errorf("error = %v, want the wrapped extractor failure", err)
		}
	})
}
