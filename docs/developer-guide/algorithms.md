# Developer Guide — Algorithms and Text Processing

This guide documents the algorithms in edub-kushim that are "real computer
science" rather than plumbing: the TextRank extractive summarizer, the text
normalization pipeline, token estimation, and the hashing utilities. It shows
the theory next to the implementation, with file references.

Audience: developers who want to understand *how* the text reduction works
(before sending content to the LLM or the tag matcher), or who need to modify
or replace any of these algorithms.

---

## Table of contents

1. [Orientation](#1-orientation)
2. [TextRank in one paragraph](#2-textrank-in-one-paragraph)
3. [Preprocessing](#3-preprocessing)
4. [Sentence splitting and chunking](#4-sentence-splitting-and-chunking)
5. [TF-IDF chunk scoring](#5-tf-idf-chunk-scoring)
6. [The similarity graph and weighted PageRank](#6-the-similarity-graph-and-weighted-pagerank)
7. [Position bias](#7-position-bias)
8. [The final score blend](#8-the-final-score-blend)
9. [Greedy selection with diversity penalty](#9-greedy-selection-with-diversity-penalty)
10. [Reconstruction](#10-reconstruction)
11. [Text normalization utilities](#11-text-normalization-utilities)
12. [Token estimation](#12-token-estimation)
13. [Hashing and checksums](#13-hashing-and-checksums)
14. [Where each algorithm is used](#14-where-each-algorithm-is-used)
15. [Gotchas](#15-gotchas)

---

## 1. Orientation

The algorithms live in these places:

- `internal/tools/textreducer/textrank.go` — the extractive
  summarizer (the `TextRank` adapter behind the `textreducer` tool).
- `internal/textnorm/` — string normalization shared by tags and people
  (tag canonicalization, DB key normalization, accent folding).
- `internal/llm/analyzer/` and `internal/enrichment/` — token estimation,
  code-block cleaning, and title truncation.
- `internal/documents/storage/` — streaming MD5 and SHA-256 hashing for
  content dedup and the text-hash column.

Why they exist: the enricher must not send entire documents to the LLM (token
budgets) or to the embedding model (context limits). Instead it *reduces* the
text to a target word count, and the quality of that reduction directly
affects the quality of analysis — a bad summary means the LLM never sees the
relevant paragraphs. See `llm.md` §10 for how `ReduceContent`
feeds the pipeline, and `semantic-matching.md` for the tag
matcher's own reduction.

The knobs live in the `enricher.textreducer` config section
(`internal/config/config.go:152-156`): `engine` (`textrank`),
`timeout`, `target_words` (default 2000).

---

## 2. TextRank in one paragraph

TextRank (Mihalcea & Tarau, 2004) is PageRank applied to text. Sentences
(or, here, chunks) are the nodes of a graph; edges are weighted by how similar
two chunks are; the PageRank-style iteration makes chunks that are similar to
*important* chunks important themselves; the top-scoring chunks are then
extracted in order. This implementation adds two extras on top of the classic
algorithm: **TF-IDF weighting** as an additional signal, and a **position
bias** (documents often put the key content at the start/end), blended into a
final score; then a **greedy selection** pass that penalizes redundancy.

The whole pipeline, in `Reduce` (`textrank.go:68-139`):

```go
cleanedUpContent := cleanUp(content)
wordsArray := strings.Fields(cleanedUpContent)

chunks := createChunks(wordsArray, chunkSize)
chunks = tfScores(chunks)
chunks = calculateGraphScores(chunks)
chunks = calculateFinalScore(chunks, TfWeight, GraphWeight, PositionWeight, detectPositionBias(cleanedUpContent))
// then greedy selection with diversity penalty
```

Weights are constants at the top of `Reduce`
(`textrank.go:69-73`): TF 0.4, graph 0.4, position 0.2 — the classic
"lexical + graph + position" blend.

---

## 3. Preprocessing

Before anything else, the text is cleaned (`textrank.go:75-76`):

```go
cleanedUpContent := cleanUp(content)
wordsArray := strings.Fields(cleanedUpContent)
```

`cleanUp` (`internal/tools/textreducer/textrank.go`) strips a set of symbol
characters (`$ € £ ¥ ¢ % & * + = < > ^ | ~ @ # \ _ [ ] { }`) — they carry no
lexical signal for the sentence-graph but would pollute token frequency
counts. `strings.Fields` then splits on any whitespace runs — the project's
standard word tokenizer (simple, language-agnostic, no stemming).

---

## 4. Sentence splitting and chunking

`splitSentences` (`textrank.go:147-163`) uses a regex boundary:

```go
var sentenceBreak = regexp.MustCompile(`[.!?]\s+|\n\n`)

func splitSentences(text string) []string {
    locs := sentenceBreak.FindAllStringIndex(text, -1)
    ...
    for _, loc := range locs {
        endOfPunct := loc[0] + 1        // keep the punctuation with its sentence
        sentences = append(sentences, text[prev:endOfPunct])
        prev = loc[1]
    }
    ...
}
```

The `\n\n` alternative never matches: `createChunks` joins the `strings.Fields`
words with single spaces before splitting, so blank-line-separated text (OCR'd
lists, tables) is one run-on sentence — which would become a single chunk that
dwarfs the window and leaves the selection (§9) nothing to choose from.
`splitOversizedSentences` (`textrank.go:169-186`) therefore cuts any sentence
longer than `chunkSize` into word windows:

```go
for len(words) > chunkSize {
    out = append(out, strings.Join(words[:chunkSize], " "))
    words = words[chunkSize:]
}
```

`createChunks` (`textrank.go:188-228`) packs the sentences into chunks of
roughly `chunkSize` words, carrying the trailing sentence over as overlap:

```go
if len(currentWords)+len(sentenceWords) > chunkSize && len(currentWords) > 0 {
    lastSentence := lastSentenceWords(currentWords, overlapWords)
    chunks = append(chunks, buildChunk(chunkId, currentWords, len(lastSentence), tokenRe))
    chunkId++
    currentWords = append(lastSentence, sentenceWords...)   // overlap
    overlapWords = len(lastSentence)
} else {
    currentWords = append(currentWords, sentenceWords...)
}
```

`lastSentenceWords` (`textrank.go:263-271`) finds that trailing sentence by
scanning backward for `.`, `!` or `?`, never past the carried overlap (which
would compound window after window), falling back to the final word when the
range is punctuation-free (word windows); `OverlapWords` lets the
reconstruction (§10) drop the duplicates.

Each chunk is tokenized (`buildChunk`, `textrank.go:230-258`): words are split
on any non-letter/non-digit run
(`tokenRe := regexp.MustCompile(`[^\p{L}\p{N}]+`)`), and the chunk stores its
tokens, unique tokens, and a frequency map — all the inputs for TF scoring.

---

## 5. TF-IDF chunk scoring

`tfScores` (`textrank.go:304-345`) treats each chunk as a "document" and the
whole text as the corpus — classic TF-IDF, per chunk:

```go
for token, localFreq := range chunk.TokenFrequencies {
    df := docFreq[token]
    idf := math.Log((N + 1) / (float64(df) + 1))
    tf := float64(localFreq) / float64(len(chunk.Tokens))
    totalWeight += tf * idf
}
chunk.TFScore = totalWeight
```

The `+1` smoothing keeps `idf` defined for tokens in every chunk and
down-weights *stopword-like* tokens that appear everywhere. Document
frequency counts a token **once per chunk** (`seen` map,
`textrank.go:309-314`).

Scores are then normalized to sum to 1 across chunks
(`NormalizedTFScore`, `textrank.go:339-342`) so the three score components
are comparable in the blend.

---

## 6. The similarity graph and weighted PageRank

`buildGraph` (`textrank.go:347-371`) creates the chunk-similarity graph:

```go
// self-similarity
graph.Adjacency[i][i] = 1.0

for i := range chunksLength {
    for j := i + 1; j < chunksLength; j++ {
        similarity := jaccardSimilarity(chunks[i].UniqueTokens, chunks[j].UniqueTokens)
        if similarity > 0 {
            graph.Adjacency[i][j] = similarity
            graph.Adjacency[j][i] = similarity
        }
    }
}
```

- Similarity is **Jaccard on unique tokens** (`jaccardSimilarity`,
  `textrank.go:373-394`): `|A ∩ B| / |A ∪ B|`, with an explicit empty/empty
  → 0 guard.
- The diagonal is 1.0 (self-similarity), which anchors the graph and keeps
  every node with a nonzero outbound sum.
- Edges with similarity 0 are omitted (adjacency stays 0), so the graph is
  sparse for dissimilar chunks.

`weightedPageRank` (`textrank.go:396-451`) is the iteration, run with
**damping 0.85, max 100 iterations, tolerance 1e-4**
(`textrank.go:406-417`): scores start uniform (`1/N`), outgoing weight sums are
precomputed, and each iteration recomputes
`newScores[i] = (1−damping)/N + damping · Σ_j score[j] · (edge[j][i] / outgoingSums[j])`
— a node's importance flows to its neighbors *proportionally to edge weight*.
The random jump (`(1-damping)/N`) keeps the chain ergodic. Convergence is
checked on total absolute change.

`calculateGraphScores` (`textrank.go:453-473`) normalizes the PageRank
scores to sum 1 (guarding `sumScores > 0`).

---

## 7. Position bias

`detectPositionBias` (`textrank.go:290-302`) picks a bias profile from the
text's newline density — a cheap proxy for "structured document vs prose":

```go
density := float64(lines) / float64(chars) * 1000
if density > 20 {
    return "decay"
}
return "cosine"
```

`positionScore` (`textrank.go:277-288`) then weights a chunk's normalized
position (`i/len(chunks)`, 0 = start, 1 = end):

- **`"cosine"`** (prose): `0.5 + 0.5·|cos(pos·2π)|` — peaks at the very
  start and very end (position 0 and 1 → |cos(0)| = |cos(2π)| = 1), dipping
  in the middle. Matches the intuition that prose leads with its thesis and
  concludes with a summary.
- **`"decay"`** (structured, line-dense): `max(e^(−4·pos), e^(−4·(1−pos)))`
  — strong bias toward the beginning, weaker tail preference.

Position scores are normalized to sum 1 before blending.

---

## 8. The final score blend

`calculateFinalScore` (`textrank.go:475-499`) combines the three normalized
components with the weights 0.4 / 0.4 / 0.2:

```go
chunks[i].FinalScore = (tf_weight * chunks[i].NormalizedTFScore) +
    (graph_weight * chunks[i].NormalizedGraphScore) +
    (position_weight * normalizedPosScore)
```

All three components sum to 1 individually, so `FinalScore` is a proper
weighted average. `cmpChunk` (`textrank.go:501-503`) sorts chunks by
`FinalScore` descending (`cmp.Compare(b.FinalScore, a.FinalScore)`).

---

## 9. Greedy selection with diversity penalty

The selection loop (`textrank.go:88-114`) is a greedy knapsack with an
anti-redundancy twist:

```go
selectedChunks := make([]Chunk, 0, len(chunks))
selectedChunks = append(selectedChunks, chunks[0])        // first chunk always kept
remainingChunks := ...copy of chunks[1:]...
slices.SortFunc(remainingChunks, cmpChunk)

currentWordCount := chunks[0].WordCount

for len(remainingChunks) > 0 && currentWordCount < targetWordCount {
    selected := remainingChunks[0]                        // best remaining
    selectedChunks = append(selectedChunks, selected)
    currentWordCount += selected.WordCount

    remainingChunks = remainingChunks[1:]

    for i := range remainingChunks {
        similarity := jaccardSimilarity(selected.UniqueTokens, remainingChunks[i].UniqueTokens)
        if similarity > DiversityThreshold {              // 0.15
            penalty := 1.0 - (similarity * (1.0 - MinPenalty))   // MinPenalty 0.1
            remainingChunks[i].FinalScore *= penalty
        }
    }

    slices.SortFunc(remainingChunks, cmpChunk)            // re-sort after penalties
}
```

- **Always keep the first chunk** — the document's opening is treated as
  non-negotiable context.
- Each pick **penalizes** chunks similar to it (Jaccard > 0.15) by scaling
  their score toward `MinPenalty` (0.1): a chunk 100% similar to the pick is
  multiplied by 0.1; one at the threshold is untouched
  (`1 − 0.15·0.9 = 0.865`).
- After each pick the list is re-sorted, so the next pick is the best
  *remaining* chunk under the accumulated penalties.
- The loop stops when the target word count is reached or nothing remains —
  `targetWordCount` comes from the enricher (§14).

---

## 10. Reconstruction

`Reduce` reassembles the selected chunks back into text, in original order
(`textrank.go:116-137`):

```go
slices.SortFunc(selectedChunks, func(a, b Chunk) int {
    return cmp.Compare(a.Id, b.Id)
})

var reducedContent strings.Builder
reducedContent.WriteString(selectedChunks[0].RawText)
prevId := selectedChunks[0].Id

for _, chunk := range selectedChunks[1:] {
    if chunk.Id == prevId+1 {
        reducedContent.WriteString(" ")
        // skip the overlap words that were already emitted at the end of the previous chunk
        reducedContent.WriteString(strings.Join(chunk.Words[chunk.OverlapWords:], " "))
    } else {
        reducedContent.WriteString(" [...] ")
        reducedContent.WriteString(chunk.RawText)
    }
}
```

Adjacent selected chunks are merged seamlessly (the overlap words carried by
the later chunk are dropped — that's what `OverlapWords` is for); gaps between
non-adjacent chunks become `[...]`, a marker the LLM prompt and the tag
matcher both understand as "content omitted here". The result reads like a
continuous excerpt, which is deliberate: the downstream consumers see
plausible prose, not a list of fragments.

---

## 11. Text normalization utilities

`internal/textnorm/` holds the shared string machinery.

### `NormalizeForDB` — the canonical key normalization

Used for people names and any DB key that must be
case/accent/format-insensitive (`internal/textnorm/normalize.go`). The pipeline:
NFKC → lowercase → trim → hyphen/underscore/dash-family → space → **accent
folding** → strip non-`[a-z ]` → collapse spaces.

Accent folding is a stateless per-call fold (`internal/textnorm/normalize.go`):

```go
// FoldAccents decomposes, drops combining marks (Mn), and recomposes.
// Stateless per-call: transform.Chain is not safe for concurrent use.
func FoldAccents(s string) string {
    decomp := norm.NFD.String(s)
    rs := []rune(decomp)
    out := make([]rune, 0, len(rs))
    for _, r := range rs {
        if !unicode.Is(unicode.Mn, r) {
            out = append(out, r)
        }
    }
    return norm.NFC.String(string(out))
}
```

This is the standard Unicode trick: decompose, remove the combining-class
marks, recompose — "José" and "Jose" normalize identically. The fold is built
from per-call `norm.Form.String` primitives rather than a shared
`transform.Chain` because `transform.Chain` carries cross-call state and is
not safe for concurrent use — this is the canonical pattern to copy.

### `NormalizeTag` — the canonical tag name

Tag names have their own normalizer (`internal/textnorm/tagname.go`): NFKC → lowercase → trim →
dash family (`-`, `_`, en/em dash, `\u2010`, `\u2011`, `\uFF0D`) → space →
accent folding → strip everything except letters of any Unicode script,
decimal digits, and `+ # .` → collapse spaces. A result with no letter and no
digit is rejected (returns `""`, which callers treat as invalid), so `"..."`
never becomes a tag. Unlike `NormalizeForDB` it does **not** restrict to
`[a-z ]`: `c++`, `c#`, `asp.net`, `3d`, and `история` survive as themselves.

It is applied at the tag save chokepoints (`curation.Tag.Create`/`Update`) and
by the enrichment pipeline through `contentanalyzer.normalizeCore`/
`NormalizeTags`. The embedding cache and the matcher consume names verbatim —
no per-key re-normalization — so store keys always equal DB names.

### `truncate` — rune-safe, with a sentinel

`truncate(s, maxLength)` (`internal/enrichment/text.go`) truncates by **runes, not bytes**
(essential for CJK), trims trailing whitespace, and returns `"Unknown"` for
blank input — the project's "no title" sentinel. Titles are truncated to 127
chars before persisting (the LLM prompt demands the same limit).

### `cleanCodeBlock` — LLM output cleanup

`cleanCodeBlock` (`internal/llm/analyzer/text.go`) strips ` ```json ` / ` ``` ` fences and
trims — the pre-step before `json.Unmarshal` of LLM responses. See
`llm.md` §7.

### `stripTags` — byte-level HTML stripping

`stripTags` (`internal/api/handlers/text.go`) walks bytes, skipping from `<` to the next
`>` — a hand-rolled, allocation-light alternative to regex HTML removal for
OCR/noisy text.

---

## 12. Token estimation

LLM token counting without a tokenizer (`internal/llm/analyzer/text.go`):

```go
func estimateTokens(text string) int {
    var total, cjk int
    for _, r := range text {
    total++
        if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
            unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
            cjk++
        }
    }
    ...
    if float64(cjk)/float64(total) > 0.10 {
        return int(math.Ceil(float64(cjk)*1.5 + float64(total-cjk)/4.0))
    }
    return int(math.Ceil(float64(total) / 4.0))
}
```

- Latin-heavy text: ~4 characters per token (the common rule of thumb).
- CJK-heavy text (>10% Han/Hiragana/Katakana/Hangul): CJK chars count 1.5
  tokens each (they tokenize near 1:1 or worse), other chars 0.25.

This feeds the LLM pre-flight check (`checkContentTooLarge`) — the enricher
shrinks the reduced text if the estimate exceeds the model's context window.

---

## 13. Hashing and checksums

`internal/documents/storage/files.go` and `internal/documents/consumption/storage.go` stream file contents into hashers:

```go
// streaming MD5: io.Copy into the hasher
func CalculateMD5(path string) (string, error) { ... }
```

Both **MD5 and SHA-512** are computed per ingested file. MD5 is the dedup
key for the consume task (`"consume:" + md5` — see
`task-system.md` §9); SHA-512 is the stronger uniqueness
check stored as `document.sha512_checksum UNIQUE`. Streaming (`io.Copy`)
keeps memory flat for multi-GB files. `ListFilePaths` additionally sorts by
**ctime** (`syscall.Stat_t.Ctim`) and filters by MIME sniffing
(`mimetype.DetectFile`), with a `maxFiles` cap.

---

## 14. Where each algorithm is used

| Algorithm / utility | Caller | Purpose |
|---|---|---|
| `TextRank.Reduce` | `tools.Runner.ReduceContent` (`internal/tools/runner.go:503-531`) | shrink text to `target_word_count` before the LLM (`enricher.textreducer.target_words`) and the tag matcher (`enricher.tagmatcher.reduce_target_words`) |
| `TargetWordCount` | `internal/matcher/client.go:49-55` | negative config = fraction of document; floor 2000 |
| `NormalizeForDB` | `internal/enrichment/enricher.go:426`, people/curation services | canonical DB keys for dedup/lookup |
| `estimateTokens` | `analyzer/gates.go` `checkContentTooLarge` | LLM pre-flight budget check |
| `truncate` | `internal/enrichment/enricher.go:709-724` | title ≤ 127 chars |
| `cleanCodeBlock` | both LLM adapters | strip fences before JSON parse |
| `CalculateMD5` / SHA-512 | `internal/documents/consumption/scan.go` | content dedup + uniqueness |
| `stripTags` | textextractor output | remove HTML noise from extracted text |

The enricher also has a second reduction path: when the LLM rejects the text
as too large, it re-reduces with a scaled-down target
(`enricher.go:120-168`, ratio = budget/estimate × 0.9) and retries once — the
token estimate is what drives that loop. See
`llm.md` §10.

---

## 15. Gotchas

- **`Reduce` returns the original content unchanged** when there are no
  chunks (`textrank.go:80-82`) — empty input is not an error.
- **The first chunk is unconditionally kept** (§9). For documents with
  front-matter noise (copyright pages), that noise is always in the
  reduction.
- **Position normalization with one chunk**: `normalize(0, 1) = 0` → cosine
  score 1.0 — a single-chunk document always gets the maximum position
  weight, which is fine (there's nothing else).
- **Jaccard on token sets ignores frequency** — "the" repeated 50× weighs the
  same as once. That's mitigated upstream by `cleanUp` (symbols) but stopwords
  are not removed anywhere; TF-IDF's idf term handles them in scoring, not in
  similarity.
- **`sentenceBreak` is Latin-centric** (`[.!?]`); CJK text (no spaces, 。
  full stops) fragments differently. The CJK-aware code paths are in
  *token estimation* and *normalization*, not in sentence splitting.
- **`splitSentences` keeps punctuation attached** (`endOfPunct := loc[0]+1`),
  which is why `lastSentenceWords` can detect sentence ends by trailing `.`
  `!` `?`.
- **The diversity penalty is multiplicative and compounding** — a chunk
  similar to two already-selected chunks gets penalized twice. That's
  intended (redundancy kills value), but it makes thresholds sensitive:
  lowering `DiversityThreshold` (0.15) to 0.1 noticeably changes the output.
- **`estimateTokens` is an estimate** — real tokenizer counts can differ
  ±30%. The enricher's retry loop absorbs the error; don't tighten budgets to
  the estimate's exact value.
- **`truncate` returns `"Unknown"` for whitespace-only input** — callers must
  not assume a non-empty return.
- **Regex-heavy helpers are compiled at package level** (`sentenceBreak`,
  `tokenRe`, `nonAlphaSpace`, `multiSpace`, `spaceRE`) — never inside the
  per-chunk loops, where compilation would dominate runtime.

---

*Last verified against the tree: 2026-09-11. If code and doc disagree, code wins.*
