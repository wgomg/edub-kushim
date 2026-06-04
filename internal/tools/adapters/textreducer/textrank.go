package textreducer

import (
	"cmp"
	"context"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/utils"
)

type TextRank struct {
	logger *utils.Logger
	config config.ToolConfig
}

type Chunk struct {
	Id                   int
	NormalizedPosition   float64
	RawText              string
	Words                []string
	StartWordIndex       int
	EndWordIndex         int
	WordCount            int
	OverlapWords         int
	Tokens               []string
	UniqueTokens         []string
	TokenFrequencies     map[string]int
	TFScore              float64
	NormalizedTFScore    float64
	GraphScore           float64
	NormalizedGraphScore float64
	FinalScore           float64
}

type Node struct {
	ID    int
	Chunk *Chunk
	Score float64
}

type Graph struct {
	Nodes     []*Node
	Adjacency [][]float64
}

func NewTextRank(logger *utils.Logger, cfg config.ToolConfig) (*TextRank, error) {
	return &TextRank{logger: logger, config: cfg}, nil
}

func (t *TextRank) Reduce(ctx context.Context, content string, chunkSize int, targetWordCount int) (*string, error) {
	TfWeight := 0.4
	GraphWeight := 0.4
	PositionWeight := 0.2
	DiversityThreshold := 0.15
	MinPenalty := 0.1

	cleanedUpContent := utils.CleanUp(content)
	wordsArray := strings.Fields(cleanedUpContent)

	chunks := createChunks(wordsArray, chunkSize)

	if len(chunks) == 0 {
		return &content, nil
	}

	chunks = tfScores(chunks)
	chunks = calculateGraphScores(chunks)
	chunks = calculateFinalScore(chunks, TfWeight, GraphWeight, PositionWeight, detectPositionBias(cleanedUpContent))

	selectedChunks := make([]Chunk, 0, len(chunks))
	selectedChunks = append(selectedChunks, chunks[0])

	remainingChunks := make([]Chunk, len(chunks)-1)
	copy(remainingChunks, chunks[1:])
	slices.SortFunc(remainingChunks, cmpChunk)

	currentWordCount := chunks[0].WordCount

	for len(remainingChunks) > 0 && currentWordCount < targetWordCount {
		selected := remainingChunks[0]
		selectedChunks = append(selectedChunks, selected)
		currentWordCount += selected.WordCount

		remainingChunks = remainingChunks[1:]

		for i := range remainingChunks {
			similarity := jaccardSimilarity(selected.UniqueTokens, remainingChunks[i].UniqueTokens)

			if similarity > DiversityThreshold {
				penalty := 1.0 - (similarity * (1.0 - MinPenalty))
				remainingChunks[i].FinalScore *= penalty
			}
		}

		slices.SortFunc(remainingChunks, cmpChunk)
	}

	slices.SortFunc(selectedChunks, func(a, b Chunk) int {
		return cmp.Compare(a.Id, b.Id)
	})

	var reducedContent strings.Builder
	reducedContent.WriteString(selectedChunks[0].RawText)
	prevId := selectedChunks[0].Id

	for _, chunk := range selectedChunks[1:] {
		if chunk.Id == prevId+1 {
			reducedContent.WriteString(" ")
			reducedContent.WriteString(strings.Join(chunk.Words[chunk.OverlapWords:], " "))
		} else {
			reducedContent.WriteString(" [...] ")
			reducedContent.WriteString(chunk.RawText)
		}
		prevId = chunk.Id
	}

	result := reducedContent.String()

	return &result, nil
}

func (t *TextRank) Name() string {
	return "textrank"
}

var sentenceBreak = regexp.MustCompile(`[.!?]\s+|\n\n`)

func splitSentences(text string) []string {
	locs := sentenceBreak.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return []string{text}
	}
	var sentences []string
	prev := 0
	for _, loc := range locs {
		endOfPunct := loc[0] + 1
		sentences = append(sentences, text[prev:endOfPunct])
		prev = loc[1]
	}
	if prev < len(text) {
		sentences = append(sentences, text[prev:])
	}
	return sentences
}

func createChunks(words []string, chunkSize int) []Chunk {
	rawText := strings.Join(words, " ")
	sentences := splitSentences(rawText)

	tokenRe := regexp.MustCompile(`[^\p{L}\p{N}]+`)

	var chunks []Chunk
	var currentWords []string
	chunkId := 0

	for _, sentence := range sentences {
		sentenceWords := strings.Fields(sentence)
		if len(sentenceWords) == 0 {
			continue
		}

		if len(currentWords)+len(sentenceWords) > chunkSize && len(currentWords) > 0 {
			lastSentence := lastSentenceWords(currentWords)
			chunks = append(chunks, buildChunk(chunkId, currentWords, len(lastSentence), tokenRe))
			chunkId++

			currentWords = append(lastSentence, sentenceWords...)
		} else {
			currentWords = append(currentWords, sentenceWords...)
		}
	}

	if len(currentWords) > 0 {
		chunks = append(chunks, buildChunk(chunkId, currentWords, 0, tokenRe))
	}

	total := float64(len(chunks))
	for i := range chunks {
		chunks[i].NormalizedPosition = normalize(float64(i), total)
	}

	return chunks
}

func buildChunk(id int, words []string, overlapWords int, tokenRe *regexp.Regexp) Chunk {
	var tokens []string
	for _, w := range words {
		parts := tokenRe.Split(w, -1)
		for _, p := range parts {
			if p != "" {
				tokens = append(tokens, p)
			}
		}
	}

	tokenFrequencies := make(map[string]int)
	for _, t := range tokens {
		tokenFrequencies[t]++
	}

	return Chunk{
		Id:               id,
		Words:            words,
		RawText:          strings.Join(words, " "),
		StartWordIndex:   0,
		EndWordIndex:     len(words),
		WordCount:        len(words),
		OverlapWords:     overlapWords,
		Tokens:           tokens,
		UniqueTokens:     slices.Collect(maps.Keys(tokenFrequencies)),
		TokenFrequencies: tokenFrequencies,
	}
}

func lastSentenceWords(words []string) []string {
	for i := len(words) - 1; i >= 0; i-- {
		w := words[i]
		if strings.HasSuffix(w, ".") || strings.HasSuffix(w, "!") || strings.HasSuffix(w, "?") {
			return words[i:]
		}
	}
	return words
}

func normalize(i float64, max float64) float64 {
	return i / max
}

func positionScore(pos float64, bias string) float64 {
	switch bias {
	case "cosine":
		s := math.Cos(pos * math.Pi * 2)
		s = math.Abs(s)
		return 0.5 + (s * 0.5)
	default: // "decay"
		startWeight := math.Exp(-pos * 4)
		endWeight := math.Exp(-(1 - pos) * 4)
		return math.Max(startWeight, endWeight)
	}
}

func detectPositionBias(text string) string {
	lines := strings.Count(text, "\n")
	chars := len(text)
	if chars == 0 {
		return "decay"
	}
	density := float64(lines) / float64(chars) * 1000

	if density > 20 {
		return "decay"
	}
	return "cosine"
}

func tfScores(chunks []Chunk) []Chunk {
	N := float64(len(chunks))

	docFreq := make(map[string]int)
	for _, chunk := range chunks {
		seen := make(map[string]bool)
		for token := range chunk.TokenFrequencies {
			if !seen[token] {
				docFreq[token]++
				seen[token] = true
			}
		}
	}

	var tfScores []float64
	for i := range chunks {
		chunk := &chunks[i]
		totalWeight := 0.0

		for token, localFreq := range chunk.TokenFrequencies {
			df := docFreq[token]
			idf := math.Log((N + 1) / (float64(df) + 1))
			tf := float64(localFreq) / float64(len(chunk.Tokens))
			totalWeight += tf * idf
		}

		chunk.TFScore = totalWeight
		tfScores = append(tfScores, chunk.TFScore)
	}

	sumTF := 0.0
	for _, score := range tfScores {
		sumTF += score
	}

	for i := range chunks {
		chunk := &chunks[i]
		chunk.NormalizedTFScore = chunk.TFScore / sumTF
	}

	return chunks
}

func buildGraph(chunks []Chunk) Graph {
	chunksLength := len(chunks)
	graph := Graph{Nodes: make([]*Node, chunksLength), Adjacency: make([][]float64, chunksLength)}

	for i := range graph.Adjacency {
		graph.Adjacency[i] = make([]float64, chunksLength)
		graph.Nodes[i] = &Node{ID: i, Chunk: &chunks[i], Score: 0.0}

		// self-similarity
		graph.Adjacency[i][i] = 1.0
	}

	for i := range chunksLength {
		for j := i + 1; j < chunksLength; j++ {
			similarity := jaccardSimilarity(chunks[i].UniqueTokens, chunks[j].UniqueTokens)

			if similarity > 0 {
				graph.Adjacency[i][j] = similarity
				graph.Adjacency[j][i] = similarity
			}
		}
	}

	return graph
}

func jaccardSimilarity(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0.0
	}

	set := make(map[string]bool)
	for _, v := range a {
		set[v] = true
	}

	intersection := 0
	for _, v := range b {
		if set[v] {
			intersection++
		}
	}

	// union = |A| + |B| - intersection
	union := len(a) + len(b) - intersection

	return float64(intersection) / float64(union)
}

func weightedPageRank(
	graph Graph,
	damping float64,
	maxIterations int,
	tolerance float64,
) []float64 {
	N := len(graph.Nodes)

	scores := make([]float64, N)
	for i := range scores {
		scores[i] = 1.0 / float64(N)
	}

	// precompute outgoing weigh sums for each node
	outgoingSums := make([]float64, N)
	for i := range N {
		sum := 0.0
		for j := range N {
			sum += graph.Adjacency[i][j]
		}
		outgoingSums[i] = sum
	}

	for range maxIterations {
		newScores := make([]float64, N)
		totalChange := 0.0

		randomComponent := (1.0 - damping) / float64(N)

		for i := range N {
			linkComponent := 0.0

			for j := range N {
				if i == j {
					continue
				}

				weight := graph.Adjacency[j][i]
				if weight > 0 && outgoingSums[j] > 0 {
					linkComponent += scores[j] * (weight / outgoingSums[j])
				}
			}

			newScores[i] = randomComponent + (damping * linkComponent)
			totalChange += math.Abs(newScores[i] - scores[i])
		}

		if totalChange < tolerance {
			break
		}

		scores = newScores
	}

	return scores
}

func calculateGraphScores(chunks []Chunk) []Chunk {
	graph := buildGraph(chunks)
	scores := weightedPageRank(graph, 0.85, 100, 0.0001)

	for i := range chunks {
		chunks[i].GraphScore = scores[i]
	}

	sumScores := 0.0
	for _, chunk := range chunks {
		sumScores += chunk.GraphScore
	}

	if sumScores > 0 {
		for i := range chunks {
			chunks[i].NormalizedGraphScore = chunks[i].GraphScore / sumScores
		}
	}

	return chunks
}

func calculateFinalScore(chunks []Chunk, tf_weight, graph_weight, position_weight float64, positionBias string) []Chunk {
	positionScores := make([]float64, len(chunks))
	for i := range chunks {
		positionScores[i] = positionScore(chunks[i].NormalizedPosition, positionBias)
	}

	// normalize scores to sum 1.0
	sumPos := 0.0
	for _, score := range positionScores {
		sumPos += score
	}

	for i := range chunks {
		normalizedPosScore := 0.0
		if sumPos > 0 {
			normalizedPosScore = positionScores[i] / sumPos
		}

		chunks[i].FinalScore = (tf_weight * chunks[i].NormalizedTFScore) +
			(graph_weight * chunks[i].NormalizedGraphScore) +
			(position_weight * normalizedPosScore)
	}

	return chunks
}

func cmpChunk(a, b Chunk) int {
	return cmp.Compare(b.FinalScore, a.FinalScore)
}
