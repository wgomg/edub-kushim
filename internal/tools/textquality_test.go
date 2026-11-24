package tools

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

// mojibakeFromLatin1 builds a wrong-charset fixture: text encoded with its real charset, then read as
// ISO-8859-1. A nil encoding keeps the text's UTF-8 bytes, modelling a doubly encoded UTF-8 document.
func mojibakeFromLatin1(t *testing.T, text string, enc encoding.Encoding) string {
	t.Helper()
	raw := []byte(text)
	if enc != nil {
		var err error
		raw, err = enc.NewEncoder().Bytes(raw)
		if err != nil {
			t.Fatalf("encode fixture: %v", err)
		}
	}
	garbled, err := charmap.ISO8859_1.NewDecoder().String(string(raw))
	if err != nil {
		t.Fatalf("decode fixture as ISO-8859-1: %v", err)
	}
	return garbled
}

// TestRepairGarbledText pins the observable contract of the garble pass: clean text in any script passes
// through, lossless charset swaps are recovered, and anything lossy or ambiguous is left for the OCR fallback
// rather than risking silently wrong text.
func TestRepairGarbledText(t *testing.T) {
	const productionSample = `¹¸£ºg»´¸´¼²½£ºw¾¬¿´±£À<Á8»u±²½uÂ²¼²ÃkÄgÅAÁiÅ´¾¶²ÇxÈiÂ£¾²±´°¶g»kÞkßàÁá°²±kÊ¿ÌÓÍi«\Ï§¬ÓÍi«8Ô^®k¨ªÏÍi§®´ÖÍiÒ¹¨ãâLäå}æçèké¬êsëkìí7îZìÎèwïäðñèkònåuó¹Ï§¬ËxÚÒ¬Ù¬Ï§¬¯WÙ¬Íuô\§¬ÚÍ£®iÙ®´`

	const englishText = "The quick brown fox jumps over the lazy dog. This is a normal English document."
	const spanishText = "El rápido zorro marrón salta sobre el perro perezoso. ¿Dónde está la biblioteca?"
	const russianText = "Быстрая коричневая лиса прыгает через ленивую собаку. Это обычный русский текст."
	const arabicText = "الثعلب البني السريع يقفز فوق الكلب الكسول. هذا نص عربي عادي."
	const chineseText = "敏捷的棕色狐狸跳过了懒惰的狗。这是一段普通的中文文本，用于测试字符集检测。"
	const traditionalText = "敏捷的棕色狐狸跳過了懶惰的狗。這是一段普通的中文文本，用於測試字符集檢測。"
	const japaneseText = "素早い茶色のキツネが怠け者の犬を飛び越えます。これは普通の日本語です。"
	const koreanText = "빠른 갈색 여우가 게으른 개를 뛰어넘습니다. 이것은 평범한 한국어입니다."

	gbkGarbled := mojibakeFromLatin1(t, chineseText, simplifiedchinese.GBK)
	big5Garbled := mojibakeFromLatin1(t, traditionalText, traditionalchinese.Big5)
	shiftJISGarbled := mojibakeFromLatin1(t, japaneseText, japanese.ShiftJIS)
	utf8Garbled := mojibakeFromLatin1(t, chineseText, nil)
	lossy := strings.Repeat("abc \uFFFD ", 20)

	tests := []struct {
		name     string
		in       string
		want     garbleOutcome
		wantText string
	}{
		{"clean English passes through", englishText, garbleClean, englishText},
		{"clean Spanish passes through", spanishText, garbleClean, spanishText},
		{"clean Russian passes through", russianText, garbleClean, russianText},
		{"clean Arabic passes through", arabicText, garbleClean, arabicText},
		{"clean Chinese passes through", chineseText, garbleClean, chineseText},
		{"clean Japanese passes through", japaneseText, garbleClean, japaneseText},
		{"clean Korean passes through", koreanText, garbleClean, koreanText},
		{"empty text is clean", "", garbleClean, ""},
		{"production sample is unrecoverable", productionSample, garbleUnrecoverable, productionSample},
		{"replacement characters cannot be reversed", lossy, garbleUnrecoverable, lossy},
		{"GBK bytes read as Latin-1 reverse losslessly", gbkGarbled, garbleRepaired, chineseText},
		{"Big5 bytes read as Latin-1 reverse losslessly", big5Garbled, garbleRepaired, traditionalText},
		{"UTF-8 bytes read as Latin-1 reverse losslessly", utf8Garbled, garbleRepaired, chineseText},
		{"ambiguous Japanese decoding is not accepted", shiftJISGarbled, garbleUnrecoverable, shiftJISGarbled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotText, outcome, _ := repairGarbledText(tt.in)
			if outcome != tt.want {
				t.Errorf("outcome = %d, want %d", outcome, tt.want)
			}
			if gotText != tt.wantText {
				t.Errorf("text = %q, want %q", gotText, tt.wantText)
			}
			garbled := assessGarble(tt.in).garbled
			if wantGarbled := tt.want != garbleClean; garbled != wantGarbled {
				t.Errorf("garbled = %v, want %v", garbled, wantGarbled)
			}
		})
	}
}

// TestExtractText_GarbledResult pins the runner seam the consumer relies on: repaired text must be visible
// through the result, and unrecoverable garble must be flagged so the consumer can route it to OCR.
func TestExtractText_GarbledResult(t *testing.T) {
	const chineseText = "敏捷的棕色狐狸跳过了懒惰的狗。这是一段普通的中文文本，用于测试字符集检测。"
	const productionSample = `¹¸£ºg»´¸´¼²½£ºw¾¬¿´±£À<Á8»u±²½uÂ²¼²ÃkÄgÅAÁiÅ´¾¶²ÇxÈiÂ£¾²±´°¶g»kÞkßàÁá°²±kÊ¿ÌÓÍi«\Ï§¬ÓÍi«8Ô^®k¨ªÏÍi§®´ÖÍiÒ¹¨ãâLäå}æçèké¬êsëkìí7îZìÎèwïäðñèkònåuó¹Ï§¬ËxÚÒ¬Ù¬Ï§¬¯WÙ¬Íuô\§¬ÚÍ£®iÙ®´`

	t.Run("repaired text is visible through the result", func(t *testing.T) {
		garbled := mojibakeFromLatin1(t, chineseText, simplifiedchinese.GBK)
		r := &Runner{logger: log.NewDiscardLogger(), config: &config.Config{}, textExtractor: &stubTextExtractor{text: &garbled}}

		result, err := r.ExtractText(context.Background(), newTestPDF(t), "application/pdf")
		if err != nil {
			t.Fatalf("ExtractText: %v", err)
		}
		if result.Text == nil || *result.Text != chineseText {
			t.Errorf("text = %v, want the reversed Chinese text", result.Text)
		}
		if result.Garbled {
			t.Error("repaired text must not be flagged Garbled")
		}
	})

	t.Run("unrecoverable garble is flagged for the consumer", func(t *testing.T) {
		garbled := productionSample
		r := &Runner{logger: log.NewDiscardLogger(), config: &config.Config{}, textExtractor: &stubTextExtractor{text: &garbled}}

		result, err := r.ExtractText(context.Background(), newTestPDF(t), "application/pdf")
		if err != nil {
			t.Fatalf("ExtractText: %v", err)
		}
		if !result.Garbled {
			t.Error("lossy garble must be flagged for OCR")
		}
		if result.Text == nil || *result.Text != garbled {
			t.Errorf("text = %v, want the original garbled text", result.Text)
		}
	})

	t.Run("clean text is not flagged", func(t *testing.T) {
		clean := "This is a normal English document with no encoding problems."
		r := &Runner{logger: log.NewDiscardLogger(), config: &config.Config{}, textExtractor: &stubTextExtractor{text: &clean}}

		result, err := r.ExtractText(context.Background(), newTestPDF(t), "application/pdf")
		if err != nil {
			t.Fatalf("ExtractText: %v", err)
		}
		if result.Garbled {
			t.Error("clean text must not be flagged Garbled")
		}
	})
}
