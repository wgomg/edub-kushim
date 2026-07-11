//go:build cgo

package tagmatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/backends"
	"github.com/knights-analytics/hugot/options"
	"github.com/knights-analytics/hugot/pipelines"
	"github.com/knights-analytics/hugot/util/safeconv"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type modelConfigJSON struct {
	MaxPositionEmbeddings int `json:"max_position_embeddings"`
}

type Hugot struct {
	logger           *utils.Logger
	session          *hugot.Session
	pipeline         *pipelines.FeatureExtractionPipeline
	topN             int
	minSimilarity    float64
	consolidationSim float64 // tag→tag matching (post-LLM, higher)
	chunkSize        int     // effective max tokens per chunk
	chunkOverlap     int     // overlap in tokens between consecutive chunks
	store            EmbeddingStore
}

type match struct {
	tag        string
	similarity float64
}

func NewHugot(logger *utils.Logger, tmCfg config.TagMatcherConfig, pipeName string) (*Hugot, error) {
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
	}, nil
}

const embedBatchSize = 32

func (h *Hugot) SetStore(s EmbeddingStore) {
	h.store = s
}

var embeddingSpaceRE = regexp.MustCompile(` +`)

// normalizeForEmbedding normalizes tag names before embedding.
// Applied in AddToStore and Consolidate, not in Encode (which also handles document text).
// Counterpart in internal/cache/bootstrap.go duplicates this logic — keep in sync.
func normalizeForEmbedding(s string) string {
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	s = embeddingSpaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func (h *Hugot) AddToStore(ctx context.Context, names []string) error {
	if h == nil {
		return fmt.Errorf("tag matcher not initialized")
	}
	normalized := make([]string, len(names))
	for i, n := range names {
		normalized[i] = normalizeForEmbedding(n)
	}
	names = normalized
	for i := 0; i < len(names); i += embedBatchSize {
		end := min(i+embedBatchSize, len(names))
		chunk := names[i:end]

		vecs, err := h.Encode(ctx, nil, chunk)
		if err != nil {
			h.logger.Error(nil, "hugot: add to store: encode batch %v: %v", chunk, err)
			continue
		}
		for j, name := range chunk {
			if j < len(vecs) && vecs[j] != nil {
				h.store.Add(name, vecs[j])
				h.logger.Info(nil, "hugot: add to store `%s`", name)
			}
		}
	}
	return nil
}

func (h *Hugot) RemoveFromStore(ctx context.Context, names []string) error {
	if h == nil {
		return fmt.Errorf("tag matcher not initialized")
	}
	for _, name := range names {
		h.store.Remove(name)
		h.logger.Info(nil, "hugot: remove from store `%s`", name)
	}
	return nil
}

func (h *Hugot) Close() {
	if h != nil && h.session != nil {
		h.session.Destroy()
		h.session = nil
	}
}

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

func (h *Hugot) Consolidate(ctx context.Context, docId string, queries []string) ([]string, error) {
	if h == nil {
		return nil, fmt.Errorf("tag matcher not initialized")
	}
	entries := h.store.Entries()
	if len(entries) == 0 || h.consolidationSim == 0.0 {
		return queries, nil
	}

	normalized := make([]string, len(queries))
	for i, q := range queries {
		normalized[i] = normalizeForEmbedding(q)
	}
	queries = normalized

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

func getBackendSession(tmCfg config.TagMatcherConfig, logger *utils.Logger) (*hugot.Session, error) {
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

func downloadLib(dest, soPath string, logger *utils.Logger) error {
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

	if err := exec.Command("tar", "xzf", tgzPath, "-C", dest,
		"--strip-components=2",
		"onnxruntime-linux-x64-1.26.0/lib/libonnxruntime.so.1.26.0",
	).Run(); err != nil {
		return err
	}

	return os.Rename(filepath.Join(dest, "libonnxruntime.so.1.26.0"), soPath)
}
