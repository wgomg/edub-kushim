//go:build cgo

package matcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/backends"
	"github.com/knights-analytics/hugot/options"
	"github.com/knights-analytics/hugot/pipelines"
	"github.com/knights-analytics/hugot/util/safeconv"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/log"
)

type modelConfigJSON struct {
	MaxPositionEmbeddings int `json:"max_position_embeddings"`
}

// Hugot is the daemon-side embedding engine: it loads the configured
// backend (ONNX or Go) with its model, embeds texts in token chunks sized
// from the model's max_position_embeddings, and matches them against an
// attached tag embedding store. It implements Matcher and Embedder and
// serves the matcher daemon's HTTP handlers; the pair index behind
// SimilarPairs is mutex-guarded. Built only into cgo builds.
type Hugot struct {
	logger           *log.Logger
	session          *hugot.Session
	pipeline         *pipelines.FeatureExtractionPipeline
	topN             int
	minSimilarity    float64
	consolidationSim float64 // tag→tag matching (post-LLM, higher)
	chunkSize        int     // effective max tokens per chunk
	chunkOverlap     int     // overlap in tokens between consecutive chunks
	store            EmbeddingStore

	pairsMu sync.Mutex
	pairs   map[similarPairKey]float64
}

type similarPairKey struct {
	a, b string
}

type match struct {
	tag        string
	similarity float64
}

// NewHugot loads the model pipeline and derives the effective chunk size
// from the model's max_position_embeddings: a configured chunk size beyond
// the model limit or non-positive is clamped, leaving a safety margin and a
// ten percent token overlap between chunks. It fails when the backend
// session, the model, or its config cannot be loaded.
func NewHugot(logger *log.Logger, tmCfg config.TagMatcherConfig, pipeName string) (*Hugot, error) {
	logger.Debug(nil, "tagmatcher configured: backend=%s", tmCfg.Hugot.Backend)
	session, err := getBackendSession(tmCfg, logger)
	if err != nil {
		return nil, fmt.Errorf("hugot session (%s): %w", tmCfg.Hugot.Backend, err)
	}

	modelPath := tmCfg.Hugot.ModelPath
	pipeline, err := hugot.NewPipeline(session, hugot.FeatureExtractionConfig{
		ModelPath:    modelPath,
		OnnxFilename: "model.onnx",
		Name:         pipeName,
		Options: []backends.PipelineOption[*pipelines.FeatureExtractionPipeline]{
			pipelines.WithNormalization(),
		},
	})
	if err != nil {
		session.Destroy()
		return nil, fmt.Errorf("hugot pipeline: %w", err)
	}

	topN := max(tmCfg.TopN, 0)
	minSim := max(tmCfg.MinSimilarity, 0.0)

	// Read model config to determine max_position_embeddings
	modelConfigPath := filepath.Join(tmCfg.Hugot.ModelPath, "config.json")
	maxPos, err := readMaxPositionEmbeddings(modelConfigPath)
	if err != nil {
		session.Destroy()
		return nil, fmt.Errorf("read model config: %w", err)
	}

	// Compute effective chunk size
	safetyMargin := 12
	effectiveChunkSize := tmCfg.ChunkSize
	if effectiveChunkSize <= 0 || effectiveChunkSize > maxPos-safetyMargin {
		effectiveChunkSize = maxPos - safetyMargin
	}
	chunkOverlap := effectiveChunkSize / 10

	logger.Debug(nil, "hugot: chunk_size=%d overlap=%d topN=%d min_sim=%.2f",
		effectiveChunkSize, chunkOverlap, topN, minSim)

	return &Hugot{
		logger:           logger,
		session:          session,
		pipeline:         pipeline,
		topN:             topN,
		minSimilarity:    minSim,
		chunkSize:        effectiveChunkSize,
		chunkOverlap:     chunkOverlap,
		consolidationSim: tmCfg.ConsolidationSimilarity,
		pairs:            make(map[similarPairKey]float64),
	}, nil
}

const embedBatchSize = 32

const rankCandidates = 2

// SetStore attaches the tag embedding store that Match, Rank and
// SimilarPairs read from. It must be called before the daemon serves
// requests.
func (h *Hugot) SetStore(s EmbeddingStore) {
	h.store = s
	h.rebuildPairs()
}

func (h *Hugot) rebuildPairs() {
	h.pairsMu.Lock()
	defer h.pairsMu.Unlock()
	h.pairs = make(map[similarPairKey]float64)
	if h.consolidationSim == 0.0 {
		return
	}
	entries := h.store.Entries()
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			sim := cosineSimilarity(entries[names[i]], entries[names[j]])
			if sim >= h.consolidationSim {
				h.pairs[canonicalPairKey(names[i], names[j])] = sim
			}
		}
	}
}

// AddToStore embeds the named tags and adds them to the store and the pair
// index. AddToStore and RemoveFromStore are what keeps the daemon's view of
// the tag vocabulary current as tags are created, promoted and deleted.
func (h *Hugot) AddToStore(ctx context.Context, names []string) error {
	if h == nil {
		return fmt.Errorf("tag matcher not initialized")
	}
	for i := 0; i < len(names); i += embedBatchSize {
		end := min(i+embedBatchSize, len(names))
		chunk := names[i:end]

		vecs, err := h.Encode(ctx, nil, chunk)
		if err != nil {
			h.logger.Error(nil, "hugot: add to store: encode batch %v: %v", chunk, err)
			continue
		}

		var existing map[string][]float32
		if h.consolidationSim != 0.0 {
			existing = h.store.Entries()
		}
		for j, name := range chunk {
			if j < len(vecs) && vecs[j] != nil {
				h.store.Add(name, vecs[j])
				h.addPairsFor(name, vecs[j], chunk, vecs, existing)
				h.logger.Info(nil, "hugot: add to store `%s`", name)
			}
		}
	}
	return nil
}

func (h *Hugot) addPairsFor(name string, emb []float32, chunk []string, vecs [][]float32, existing map[string][]float32) {
	if h.consolidationSim == 0.0 {
		return
	}
	h.pairsMu.Lock()
	defer h.pairsMu.Unlock()
	for k := range h.pairs {
		if k.a == name || k.b == name {
			delete(h.pairs, k)
		}
	}
	for other, otherEmb := range existing {
		if other == name {
			continue
		}
		sim := cosineSimilarity(emb, otherEmb)
		if sim >= h.consolidationSim {
			h.pairs[canonicalPairKey(name, other)] = sim
		}
	}
	for k, otherEmb := range vecs {
		if otherEmb == nil || chunk[k] == name {
			continue
		}
		key := canonicalPairKey(name, chunk[k])
		sim := cosineSimilarity(emb, otherEmb)
		if sim >= h.consolidationSim {
			h.pairs[key] = sim
		} else {
			// batch members may have been compared above with their pre-batch
			// embeddings; drop the pair when the fresh embedding no longer qualifies
			delete(h.pairs, key)
		}
	}
}

// RemoveFromStore drops the named tags from the store and the pair index.
func (h *Hugot) RemoveFromStore(ctx context.Context, names []string) error {
	if h == nil {
		return fmt.Errorf("tag matcher not initialized")
	}
	for _, name := range names {
		h.store.Remove(name)
		h.removePairsFor(name)
		h.logger.Info(nil, "hugot: remove from store `%s`", name)
	}
	return nil
}

func (h *Hugot) removePairsFor(name string) {
	h.pairsMu.Lock()
	defer h.pairsMu.Unlock()
	for k := range h.pairs {
		if k.a == name || k.b == name {
			delete(h.pairs, k)
		}
	}
}

// SimilarPairs returns the pair index: every stored pair above the
// consolidation similarity, as maintained by AddToStore and RemoveFromStore.
func (h *Hugot) SimilarPairs(ctx context.Context) ([]SimilarPair, error) {
	if h == nil {
		return nil, fmt.Errorf("tag matcher not initialized")
	}
	h.pairsMu.Lock()
	defer h.pairsMu.Unlock()
	out := make([]SimilarPair, 0, len(h.pairs))
	for k, sim := range h.pairs {
		out = append(out, SimilarPair{A: k.a, B: k.b, Similarity: sim})
	}
	return out, nil
}

func canonicalPairKey(a, b string) similarPairKey {
	if a < b {
		return similarPairKey{a: a, b: b}
	}
	return similarPairKey{a: b, b: a}
}

// Close destroys the backend session; safe on a nil receiver.
func (h *Hugot) Close() {
	if h != nil && h.session != nil {
		h.session.Destroy()
		h.session = nil
	}
}

// Match embeds the document text and returns up to the configured top N
// stored tags above the document-match similarity. An empty store or a zero
// top N returns no matches with a nil error.
func (h *Hugot) Match(ctx context.Context, docId, input string) ([]string, error) {
	if h == nil {
		return nil, fmt.Errorf("tag matcher not initialized")
	}
	entries := h.store.Entries()
	if len(entries) == 0 || h.topN == 0 {
		return nil, nil
	}

	embeddings, err := h.Encode(ctx, &docId, []string{input})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 || embeddings[0] == nil {
		return nil, nil
	}
	inputEmb := embeddings[0]

	matches := h.rankMatches(inputEmb, entries, h.minSimilarity)
	topN := min(h.topN, len(matches))

	result := make([]string, topN)
	for i := range topN {
		result[i] = matches[i].tag
	}

	showN := min(15, len(matches))
	if showN > 0 {
		topScores := make([]string, showN)
		for i := range showN {
			topScores[i] = fmt.Sprintf("%s(%.3f)", matches[i].tag, matches[i].similarity)
		}
		h.logger.Info(&docId, "hugot: top-%d matches: %v", showN, topScores)
	} else {
		h.logger.Info(&docId, "hugot: no matches above min_sim=%.2f", h.minSimilarity)
	}

	return result, nil
}

// Consolidate resolves each query to the stored tag it consolidates to at
// the consolidation similarity, or to itself when nothing passes. An empty
// store or a zero similarity returns the queries unchanged.
func (h *Hugot) Consolidate(ctx context.Context, docId string, queries []string) ([]string, error) {
	if h == nil {
		return nil, fmt.Errorf("tag matcher not initialized")
	}
	entries := h.store.Entries()
	if len(entries) == 0 || h.consolidationSim == 0.0 {
		return queries, nil
	}

	out, err := h.pipeline.RunPipeline(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("encode queries: %w", err)
	}
	if len(out.Embeddings) != len(queries) {
		return queries, nil
	}

	result := make([]string, len(queries))
	for i, qEmb := range out.Embeddings {
		matches := h.rankMatches(qEmb, entries, h.consolidationSim)
		if len(matches) > 0 {
			h.logger.Info(&docId, "hugot: consolidate %q → %s (%.3f)", queries[i], matches[0].tag, matches[0].similarity)
			result[i] = matches[0].tag
		} else {
			result[i] = queries[i]
		}
	}

	return result, nil
}

// Rank returns per query the candidates above the consolidation
// similarity, at most two and best first; KeptName always carries the query
// itself. An empty store or a zero similarity returns the queries unranked.
func (h *Hugot) Rank(ctx context.Context, docId string, queries []string) ([]RankResult, error) {
	if h == nil {
		return nil, fmt.Errorf("tag matcher not initialized")
	}
	results := make([]RankResult, len(queries))
	for i, q := range queries {
		results[i] = RankResult{KeptName: q}
	}
	entries := h.store.Entries()
	if len(queries) == 0 || len(entries) == 0 || h.consolidationSim == 0.0 {
		return results, nil
	}

	out, err := h.pipeline.RunPipeline(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("encode queries: %w", err)
	}
	if len(out.Embeddings) != len(queries) {
		return results, nil
	}

	for i, qEmb := range out.Embeddings {
		matches := h.rankMatches(qEmb, entries, h.consolidationSim)
		if len(matches) > rankCandidates {
			matches = matches[:rankCandidates]
		}
		for _, m := range matches {
			results[i].Candidates = append(results[i].Candidates,
				Candidate{Tag: m.tag, Similarity: m.similarity})
		}
	}
	return results, nil
}

// Name identifies the in-process engine behind the Matcher seam.
func (h *Hugot) Name() string {
	return "hugot"
}

// Encode computes embeddings for a batch of texts. Short texts (≤ chunkSize tokens)
// are batched into a single pipeline call. Longer texts are encoded individually
// with overlapping token chunks and mean-pooling.
func (h *Hugot) Encode(ctx context.Context, docId *string, texts []string) ([][]float32, error) {
	if h == nil {
		return nil, fmt.Errorf("tag matcher not initialized")
	}
	if len(texts) == 0 {
		return nil, nil
	}

	tk := h.pipeline.Model.Tokenizer
	if tk == nil {
		return nil, fmt.Errorf("tokenizer not available")
	}

	type item struct {
		index int
		short bool
	}
	items := make([]item, len(texts))
	var shortTexts []string
	shortIdx := make([]int, 0, len(texts))

	for i, text := range texts {
		ids, err := h.tokenize(text, tk)
		if err != nil {
			return nil, fmt.Errorf("tokenize %q: %w", text, err)
		}
		if len(ids) <= h.chunkSize {
			items[i] = item{index: i, short: true}
			shortTexts = append(shortTexts, text)
			shortIdx = append(shortIdx, i)
		} else {
			items[i] = item{index: i, short: false}
		}
	}

	result := make([][]float32, len(texts))

	// Batch-encode all short texts at once.
	if len(shortTexts) > 0 {
		out, err := h.pipeline.RunPipeline(ctx, shortTexts)
		if err != nil {
			return nil, fmt.Errorf("encode batch: %w", err)
		}
		for j, idx := range shortIdx {
			result[idx] = out.Embeddings[j]
		}
	}

	// Encode long texts individually with chunking.
	for _, it := range items {
		if it.short {
			continue
		}
		emb, err := h.encodeChunked(ctx, docId, texts[it.index])
		if err != nil {
			return nil, fmt.Errorf("encode %q: %w", texts[it.index], err)
		}
		result[it.index] = emb
	}

	return result, nil
}

// encodeChunked embeds a single long input by splitting token IDs into
// overlapping chunks, embedding each chunk, and mean-pooling the results.
func (h *Hugot) encodeChunked(ctx context.Context, docId *string, input string) ([]float32, error) {
	tk := h.pipeline.Model.Tokenizer
	if tk == nil {
		return nil, fmt.Errorf("tokenizer not available")
	}

	tokenIDs, err := h.tokenize(input, tk)
	if err != nil {
		return nil, fmt.Errorf("tokenize input: %w", err)
	}

	n := len(tokenIDs)
	step := h.chunkSize - h.chunkOverlap
	if step <= 0 {
		step = h.chunkSize / 2
	}
	numChunks := ((n - 1) / step) + 1
	h.logger.Debug(docId, "hugot: chunked path — %d tokens into ~%d chunks (step=%d)", n, numChunks, step)

	var allEmbeddings [][]float32
	for i := 0; i < n; i += step {
		end := min(i+h.chunkSize, n)
		chunkIDs := tokenIDs[i:end]

		chunkText, err := backends.Decode(chunkIDs, tk)
		if err != nil {
			return nil, fmt.Errorf("decode chunk at offset %d: %w", i, err)
		}

		out, err := h.pipeline.RunPipeline(ctx, []string{chunkText})
		if err != nil {
			return nil, fmt.Errorf("encode chunk at offset %d: %w", i, err)
		}
		if len(out.Embeddings) > 0 {
			allEmbeddings = append(allEmbeddings, out.Embeddings[0])
		}
	}

	if len(allEmbeddings) == 0 {
		return nil, nil
	}

	return meanPool(allEmbeddings), nil
}

// tokenize returns the token IDs for the given input text using the Go tokenizer.
// This is the only runtime used by NewGoSession; the Rust/ORT path is omitted
// because its RustTokenizer type is an empty stub when built without cgo+ORT/XLA.
func (h *Hugot) tokenize(input string, tk *backends.Tokenizer) ([]uint32, error) {
	if tk.GoTokenizer != nil {
		output := tk.GoTokenizer.Tokenizer.EncodeWithAnnotations(input)
		return safeconv.IntSliceToUint32Slice(output.IDs), nil
	}
	if tk.RustTokenizer != nil {
		ids, _ := tk.RustTokenizer.Tokenizer.Encode(input, true)
		return ids, nil
	}
	return nil, fmt.Errorf("tokenizer not available")
}

// meanPool computes the element-wise average of a slice of embeddings.
func meanPool(embeddings [][]float32) []float32 {
	if len(embeddings) == 0 {
		return nil
	}
	dim := len(embeddings[0])
	result := make([]float32, dim)
	for _, emb := range embeddings {
		for j := range emb {
			result[j] += emb[j]
		}
	}
	n := float32(len(embeddings))
	for j := range result {
		result[j] /= n
	}
	return result
}

// rankMatches computes cosine similarity between a query embedding and all cached
// tag embeddings, returns matches sorted descending by similarity above threshold.
func (h *Hugot) rankMatches(queryEmb []float32, entries map[string][]float32, minSim float64) []match {
	matches := make([]match, 0, len(entries))
	for tag, tagEmb := range entries {
		sim := cosineSimilarity(queryEmb, tagEmb)
		if sim >= minSim {
			matches = append(matches, match{tag: tag, similarity: sim})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].similarity > matches[j].similarity
	})
	return matches
}

// cosineSimilarity computes the cosine similarity between two normalized vectors.
// Since both vectors are L2-normalized (enforced by pipeline.WithNormalization()),
// this is equivalent to a simple dot product.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

// readMaxPositionEmbeddings reads the max_position_embeddings field from a
// HuggingFace model's config.json file. Returns 512 as a safe default if the
// field is missing or unreadable.
func readMaxPositionEmbeddings(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg modelConfigJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.MaxPositionEmbeddings == 0 {
		return 0, fmt.Errorf("%s: max_position_embeddings is 0 or missing", path)
	}
	return cfg.MaxPositionEmbeddings, nil
}

func getBackendSession(tmCfg config.TagMatcherConfig, logger *log.Logger) (*hugot.Session, error) {
	switch strings.ToLower(tmCfg.Hugot.Backend) {
	// case "XLA":
	// 	session, err = hugot.NewXLASession(context.Background())
	case "ort":
		soPath := filepath.Join(tmCfg.Hugot.BackendLibPath, "libonnxruntime.so")
		if err := downloadLib(tmCfg.Hugot.BackendLibPath, soPath, logger); err != nil {
			return nil, err
		}
		logger.Debug(nil, "tagmatcher used: backend=%s", "ort")
		// WithCPUMemArena(false) disables ORT's internal CPU memory arena so that
		// intermediate tensor allocations are freed after each inference instead of
		// being retained in a growing pool. This caps idle RSS at ~2.2-2.5 GB with
		// BGE-M3 (24 layers, 1024-dim, chunk_size=4096) rather than the ~4-5 GB
		// the arena would hold after processing the largest input seen.
		//
		// WithMemPattern(false) disables shape-specific buffer pre-allocation.
		// Without this, ORT allocates buffers for every distinct tensor shape
		// encountered, compounding the arena retention.
		//
		// Trade-off: ~10-20% per-inference latency increase from re-allocation,
		// negligible in the enrichment pipeline where text extraction, OCR, and
		// LLM calls dominate.
		return hugot.NewORTSession(context.Background(),
			options.WithOnnxLibraryPath(tmCfg.Hugot.BackendLibPath),
			options.WithCPUMemArena(tmCfg.Hugot.CpuMemArena),
			options.WithMemPattern(tmCfg.Hugot.MemPattern),
		)
	default:
		logger.Debug(nil, "tagmatcher used: backend=%s", "go")
		return hugot.NewGoSession(context.Background())
	}
}

// SHA256 of onnxruntime-linux-x64-1.26.0.tgz, taken from the GitHub release
// API's asset digest and independently confirmed by hashing the downloaded
// file. Must be updated together with the version pinned in the URL below.
const onnxRuntimeTgzSHA256 = "1254da24fb389cf39dc0ff3451ab48301740ffbfcbaf646849df92f80ee92c57"

func downloadLib(dest, soPath string, logger *log.Logger) error {
	if _, err := os.Stat(soPath); err == nil {
		return nil
	}

	url := "https://github.com/microsoft/onnxruntime/releases/download/v1.26.0/onnxruntime-linux-x64-1.26.0.tgz"

	logger.Debug(nil, "ort backend selected: downloading libonnxruntime from %s ...", url)

	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}

	tgzPath := filepath.Join(dest, "onnxruntime.tgz")
	defer os.Remove(tgzPath)

	if err := exec.Command("curl", "-fsSL", "--retry", "3", "-o", tgzPath, url).Run(); err != nil {
		return err
	}

	if err := verifySHA256(tgzPath, onnxRuntimeTgzSHA256); err != nil {
		return fmt.Errorf("verify onnxruntime download: %w", err)
	}

	if err := exec.Command("tar", "xzf", tgzPath, "-C", dest,
		"--strip-components=2",
		"onnxruntime-linux-x64-1.26.0/lib/libonnxruntime.so.1.26.0",
	).Run(); err != nil {
		return err
	}

	return os.Rename(filepath.Join(dest, "libonnxruntime.so.1.26.0"), soPath)
}

func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, want)
	}
	return nil
}
