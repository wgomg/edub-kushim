# Double Tokenization & Decode→Re-encode Round-Trip in `encodeChunked`

## Context

The Hugot-based tag matcher (`internal/tools/adapters/tagmatcher/hugot.go`) encodes document text as embeddings via the `Encode()` method. For texts whose token count exceeds `chunkSize` (default 4096 after the fix, previously 8182), it delegates to `encodeChunked()`, which splits the tokens into overlapping windows, encodes each window, and mean-pools the results.

## The Waste

Two passes over the same text:

```
Pass 1: Encode() — tokenize entire input to classify it as short/long
                             ↓
Pass 2: encodeChunked() — tokenize entire input AGAIN to get token IDs
                             ↓
           For each chunk: decode token IDs → text (backends.Decode)
                             ↓
           Pass to RunPipeline, which RE-TOKENIZES the text internally
                             ↓
           ONNX inference
```

That is a three-stage round-trip for every chunk:

1. **Token IDs → Text** (`backends.Decode` on line 336): Converts the token ID slice back into a string by joining sub-word tokens.
2. **Text → Token IDs** (Hugot's internal pipeline tokenization): `RunPipeline` receives raw text and runs its own tokenizer on it, producing the same token IDs we just decoded.
3. **ONNX inference**: The actual model forward pass.

The first two stages are pure overhead — they cancel each other out logically.

## Impact

| Factor | Cost |
|---|---|
| Double tokenization of full input | 2× work for the Go/Rust tokenizer on every long text |
| Decode→Re-encode per chunk | ~(numChunks - 1) × full pipeline tokenization + string allocation |
| String garbage | Each chunk creates a decoded string that is immediately re-tokenized and discarded |

For a typical ~4000-word reduced text (~8K tokens, 2 chunks, step=3686, overlap=409):

- `Encode()` tokenizes ~30K chars → fine
- `encodeChunked()` tokenizes the same ~30K chars again → redundant
- Chunk 0: decode 4096 IDs → ~15K string, Re-tokenize → overhead
- Chunk 1: decode 4096 IDs → ~15K string, Re-tokenize → overhead

This inflates encoding latency by roughly 1.5–3 seconds per document, which was a significant fraction of the 30s timeout window that motivated the original timeout increase.

## Root Cause

The Hugot `FeatureExtractionPipeline` exposes `RunPipeline(ctx, texts []string)` — it takes raw text and runs the full tokenize→infer chain. There is no public API to inject pre-tokenized token IDs directly into the ONNX runtime. The workaround in `encodeChunked` is forced by this API constraint: the chunking logic works with token IDs (to enforce the size limit), but has to convert back to text to feed the pipeline.

Additionally, there is no information passing between `Encode` and `encodeChunked`: the tokenization done in `Encode` to count tokens is thrown away, and `encodeChunked` starts from scratch.

## Possible Fixes

### Option 1 — Pass token IDs to `encodeChunked` (recommended)

Refactor `Encode` to compute tokenization once and pass the `[]uint32` slice to `encodeChunked`, avoiding the re-tokenization of the full input. The per-chunk decode→re-encode would remain unless the pipeline API changes.

### Option 2 — Bypass decode→re-encode by patching Hugot (high effort, high reward)

Upstream a method on `FeatureExtractionPipeline` (or on the underlying `backends.InferenceSession`) that accepts pre-tokenized batches — token IDs as `[][]uint32` (or `[][]int64`) plus attention masks. This would eliminate the decode→re-encode entirely and could be reused for any chunked-encoding use case.

### Option 3 — Skip chunking for common cases (quick mitigation)

If `chunkSize` is set to a generous value (e.g., 4096) and most reduced texts fit in 1-2 chunks, the double-tokenization cost is low enough that it can be ignored. The chunking path is already fast enough after the `chunkSize` reduction and timeout increase. This is the current state.

## Code References

- `Encode()` — `/internal/tools/adapters/tagmatcher/hugot.go` l.247–308
  - Tokenization pass at l.268–272
- `encodeChunked()` — `/internal/tools/adapters/tagmatcher/hugot.go` l.312–355
  - Second tokenization pass at l.318
  - Decode→re-encode loop at l.332–348 (`backends.Decode` → `RunPipeline`)
