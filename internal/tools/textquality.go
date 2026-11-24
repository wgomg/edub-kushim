package tools

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// Detection thresholds, measured against representative clean and garbled fixtures. They are deliberately
// conservative: a clean text in any script must never trip the verdict, because the consumer answers a garble
// verdict with forced OCR, which rasterizes the pages.
const (
	lossyReplacementRatio = 0.01
	garbleNonASCIIShare   = 0.30
	garbleSymbolShare     = 0.25
	garbleMaxASCIIWords   = 0.20

	minCandidateLetterShare = 0.10
	maxCandidateFFFDShare   = 0.01
	minReversalScore        = 0.75
	minReversalMargin       = 0.10
)

// garbleOutcome reports what repairGarbledText decided for one text: the text was clean, a reversal candidate
// was accepted, or the text is garbled with no acceptable recovery.
type garbleOutcome int

const (
	garbleClean garbleOutcome = iota
	garbleRepaired
	garbleUnrecoverable
)

// garbleAssessment is the evidence behind a garble verdict: replacement-character and non-ASCII rune shares,
// the share of non-ASCII runes that are not letters of any script, the share of whitespace-separated tokens
// made only of ASCII letters and digits, and the C1 fingerprints that name the wrongly-applied decoder.
type garbleAssessment struct {
	garbled           bool
	replacementRatio  float64
	nonASCIIShare     float64
	latin1SymbolShare float64
	asciiWordShare    float64
	c1Printable       int
	c1Control         int
}

// reversal names the garble candidate that won: the decoder wrongly applied, the charset the original bytes
// actually are, and the plausibility score that placed it first.
type reversal struct {
	wrong       string
	trueCharset string
	score       float64
}

// namedEncoding pairs an encoding with the label used in logs and scoring.
type namedEncoding struct {
	name string
	enc  encoding.Encoding
}

// garbleCandidates is the candidate matrix of original charsets. Their decoded outputs separate by script
// (Han, kana, Hangul, Latin), which is what the scorer keys on; the set is a variable so tests can shrink it.
var garbleCandidates = []namedEncoding{
	{name: "gb18030", enc: simplifiedchinese.GB18030},
	{name: "big5", enc: traditionalchinese.Big5},
	{name: "shift_jis", enc: japanese.ShiftJIS},
	{name: "euc-jp", enc: japanese.EUCJP},
	{name: "euc-kr", enc: korean.EUCKR},
}

// cp1252C1 lists the code points Windows-1252 assigns to bytes 0x80-0x9F. Seeing them in decoded text
// fingerprints Windows-1252 as the wrongly-applied decoder; ISO-8859-1 would have produced raw C1 control
// characters for the same bytes.
var cp1252C1 = map[rune]struct{}{
	'\u20ac': {}, '\u201a': {}, '\u0192': {}, '\u201e': {}, '\u2026': {},
	'\u2020': {}, '\u2021': {}, '\u02c6': {}, '\u2030': {}, '\u0160': {},
	'\u2039': {}, '\u0152': {}, '\u017d': {}, '\u2018': {}, '\u2019': {},
	'\u201c': {}, '\u201d': {}, '\u2022': {}, '\u2013': {}, '\u2014': {},
	'\u02dc': {}, '\u2122': {}, '\u0161': {}, '\u203a': {}, '\u0153': {},
	'\u017e': {}, '\u0178': {},
}

// repairGarbledText inspects extracted text and attempts a lossless reversal when it looks like wrong-charset
// decode. It never guesses: a text that carries replacement characters, or candidates stay ambiguous, is
// reported unrecoverable so the caller can fall back to OCR.
func repairGarbledText(s string) (string, garbleOutcome, reversal) {
	a := assessGarble(s)
	if !a.garbled {
		return s, garbleClean, reversal{}
	}
	if a.replacementRatio >= lossyReplacementRatio {
		return s, garbleUnrecoverable, reversal{}
	}

	repaired, rev, ok := reverseGarble(s, a)
	if !ok {
		return s, garbleUnrecoverable, reversal{}
	}

	return repaired, garbleRepaired, rev
}

// assessGarble classifies one text. A text with replacement characters is garbled by definition. Without them
// the signature is valid UTF-8 whose non-ASCII runes are symbols rather than letters: every clean document in
// any script is letter-dominated, while a DBCS byte stream decoded as Latin-1 lands in the symbol, fraction
// and superscript ranges.
func assessGarble(s string) garbleAssessment {
	var a garbleAssessment
	var total, nonASCII, nonLetters, replacements int

	for _, r := range s {
		total++
		if r == utf8.RuneError {
			replacements++
		}
		if r < 0x80 {
			continue
		}
		nonASCII++
		if !unicode.IsLetter(r) {
			nonLetters++
		}
		switch {
		case r <= 0x9f:
			a.c1Control++
		default:
			if _, ok := cp1252C1[r]; ok {
				a.c1Printable++
			}
		}
	}

	if total == 0 {
		return a
	}

	a.replacementRatio = float64(replacements) / float64(total)
	a.nonASCIIShare = float64(nonASCII) / float64(total)
	if nonASCII > 0 {
		a.latin1SymbolShare = float64(nonLetters) / float64(nonASCII)
	}
	a.asciiWordShare = asciiWordShare(s)

	a.garbled = a.replacementRatio >= lossyReplacementRatio ||
		(a.nonASCIIShare >= garbleNonASCIIShare &&
			a.latin1SymbolShare >= garbleSymbolShare &&
			a.asciiWordShare <= garbleMaxASCIIWords)

	return a
}

// asciiWordShare reports the fraction of whitespace-separated tokens made only of ASCII letters and digits.
// Real prose has a high share even when it carries accents; a garbled blob whose bytes carry no word
// boundaries does not.
func asciiWordShare(s string) float64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}

	ascii := 0
	for _, field := range fields {
		plain := true
		for _, r := range field {
			if r >= 0x80 || (!unicode.IsLetter(r) && !unicode.IsDigit(r)) {
				plain = false
				break
			}
		}
		if plain {
			ascii++
		}
	}

	return float64(ascii) / float64(len(fields))
}

// reverseGarble runs the constrained candidate matrix: re-encode the text with the decoder that was wrongly
// applied, then decode the recovered bytes with each candidate charset (and as UTF-8 when they are valid
// UTF-8), score every distinct output, and accept the winner only when it clears the score floor and the
// margin over the runner-up.
func reverseGarble(s string, a garbleAssessment) (string, reversal, bool) {
	wrongs := wrongDecoders(a)
	if len(wrongs) == 0 {
		return s, reversal{}, false
	}

	byText := make(map[string]reversal)
	for _, wrong := range wrongs {
		raw, err := wrong.enc.NewEncoder().String(s)
		if err != nil {
			continue
		}
		if utf8.ValidString(raw) {
			if _, ok := byText[raw]; !ok {
				byText[raw] = reversal{wrong: wrong.name, trueCharset: "utf-8"}
			}
		}
		for _, candidate := range garbleCandidates {
			decoded, err := candidate.enc.NewDecoder().String(raw)
			if err != nil {
				continue
			}
			if _, ok := byText[decoded]; ok {
				continue
			}
			byText[decoded] = reversal{wrong: wrong.name, trueCharset: candidate.name}
		}
	}

	type scoredText struct {
		text string
		rev  reversal
	}
	var scored []scoredText
	for text, rev := range byText {
		score, ok := scoreCandidate(text)
		if !ok {
			continue
		}
		rev.score = score
		scored = append(scored, scoredText{text: text, rev: rev})
	}
	if len(scored) == 0 {
		return s, reversal{}, false
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].rev.score != scored[j].rev.score {
			return scored[i].rev.score > scored[j].rev.score
		}
		return scored[i].rev.trueCharset < scored[j].rev.trueCharset
	})

	winner := scored[0]
	if winner.rev.score < minReversalScore {
		return s, reversal{}, false
	}
	if len(scored) > 1 && winner.rev.score-scored[1].rev.score < minReversalMargin {
		return s, reversal{}, false
	}

	return winner.text, winner.rev, true
}

// wrongDecoders narrows the decoders to try from the C1 fingerprint: printable CP1252-only characters mean
// Windows-1252 produced the text, raw C1 controls mean ISO-8859-1 did, and no fingerprint or conflicting
// fingerprints leave both in play: with no C1 bytes the two decoders agree, and with both present neither can
// be ruled out.
func wrongDecoders(a garbleAssessment) []namedEncoding {
	iso := namedEncoding{name: "iso-8859-1", enc: charmap.ISO8859_1}
	windows := namedEncoding{name: "windows-1252", enc: charmap.Windows1252}

	switch {
	case a.c1Printable > 0 && a.c1Control == 0:
		return []namedEncoding{windows}
	case a.c1Control > 0 && a.c1Printable == 0:
		return []namedEncoding{iso}
	default:
		return []namedEncoding{iso, windows}
	}
}

// scoreCandidate rates one candidate decoding. It rejects outputs that are mostly not letters, carry
// replacement characters, or mix in Bopomofo letters (which appear when Chinese punctuation bytes are misread
// through Big5), then rewards writing-system coherence, low replacement share, and a plausible shape (sane
// token lengths, no control characters).
func scoreCandidate(s string) (float64, bool) {
	var total, replacements, letters, controls int

	for _, r := range s {
		total++
		if r == utf8.RuneError {
			replacements++
			continue
		}
		if isControlRune(r) {
			controls++
		}
		if unicode.IsLetter(r) {
			letters++
		}
	}

	if total == 0 || letters == 0 || float64(letters)/float64(total) < minCandidateLetterShare {
		return 0, false
	}

	fffdShare := float64(replacements) / float64(total)
	if fffdShare >= maxCandidateFFFDShare {
		return 0, false
	}

	coherence, ok := letterCoherence(s)
	if !ok {
		return 0, false
	}

	shape := 0.0
	if mean := meanTokenLength(s); mean >= 1 && mean <= 15 {
		shape += 0.5
	}
	if float64(controls)/float64(total) <= 0.001 {
		shape += 0.5
	}

	return 0.5*coherence + 0.3*(1-fffdShare) + 0.2*shape, true
}

// letterCoherence reports how much of a candidate's letter mass fits one writing system: Han alone, Han with
// kana (Japanese), Hangul with optional Han (Korean), or a single non-CJK script. Bopomofo does not occur in
// ordinary prose and surfaces when Chinese punctuation bytes pass through Big5, so its presence rejects the
// candidate outright.
func letterCoherence(s string) (float64, bool) {
	var letters, han, kana, hangul, bopomofo int
	var others [nonCJKScriptCount]int

	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		switch {
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.Is(unicode.Bopomofo, r):
			bopomofo++
		case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			kana++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		default:
			others[nonCJKScript(r)]++
		}
	}

	if letters == 0 || bopomofo > 0 {
		return 0, false
	}

	switch {
	case kana > 0:
		return float64(han+kana) / float64(letters), true
	case hangul > 0:
		return float64(han+hangul) / float64(letters), true
	case han > 0:
		return float64(han) / float64(letters), true
	}

	largest := 0
	for _, n := range others {
		if n > largest {
			largest = n
		}
	}

	return float64(largest) / float64(letters), true
}

// nonCJKScriptCount is the number of buckets nonCJKScript can return: one per listed script plus one shared
// bucket for the rest.
const nonCJKScriptCount = 8

// nonCJKScript buckets a non-CJK letter into one of the writing systems the candidate matrix can emit;
// unlisted scripts share the trailing bucket.
func nonCJKScript(r rune) int {
	switch {
	case unicode.Is(unicode.Latin, r):
		return 0
	case unicode.Is(unicode.Cyrillic, r):
		return 1
	case unicode.Is(unicode.Greek, r):
		return 2
	case unicode.Is(unicode.Arabic, r):
		return 3
	case unicode.Is(unicode.Hebrew, r):
		return 4
	case unicode.Is(unicode.Thai, r):
		return 5
	case unicode.Is(unicode.Devanagari, r):
		return 6
	}
	return 7
}

// isControlRune reports whether r is a control character other than the common whitespace ones.
func isControlRune(r rune) bool {
	switch r {
	case '\n', '\r', '\t':
		return false
	case 0x7f:
		return true
	}
	return r < 0x20 || (r >= 0x80 && r <= 0x9f)
}

// meanTokenLength returns the mean rune length of the whitespace-separated tokens; zero when the text has no
// tokens.
func meanTokenLength(s string) float64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}

	runes := 0
	for _, field := range fields {
		runes += utf8.RuneCountInString(field)
	}

	return float64(runes) / float64(len(fields))
}
