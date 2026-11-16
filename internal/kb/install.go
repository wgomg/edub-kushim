package kb

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/wgomg/edub-kushim/internal/database"
)

// the artifact's fixed entry names: the manifest is the metadata, so the TSVs carry no header row.
const (
	artifactManifest = "manifest.json"
	artifactConcepts = "concepts.tsv"
	artifactLemmas   = "lemmas.tsv"
	artifactEdges    = "edges.tsv"
)

// insertBatchSize bounds each row-set insert, keeping the marshalled JSON of one statement small enough to
// stay well inside the driver's parameter budget.
const insertBatchSize = 5000

// manifest is one artifact's provenance record and the license carrier: it names the upstream, its hash and
// license, the converter that produced the rows, and the row counts the installer verifies. The notice text
// is the redistribution obligation both CC licenses impose; the loader refuses an artifact without it.
type manifest struct {
	Source           string         `json:"source"`
	SourceVersion    string         `json:"source_version"`
	UpstreamURL      string         `json:"upstream_url"`
	UpstreamSHA256   string         `json:"upstream_sha256"`
	License          string         `json:"license"`
	Notice           string         `json:"notice"`
	ConverterVersion string         `json:"converter_version"`
	Filters          string         `json:"filters"`
	Counts           manifestCounts `json:"counts"`
	GeneratedAt      string         `json:"generated_at"`
}

// manifestCounts is the row count of each artifact file.
type manifestCounts struct {
	Concepts int64 `json:"concepts"`
	Lemmas   int64 `json:"lemmas"`
	Edges    int64 `json:"edges"`
}

// validate rejects an artifact whose manifest cannot stand as its provenance and license record, so an
// incomplete artifact never reaches the store.
func (m manifest) validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"source", m.Source},
		{"source_version", m.SourceVersion},
		{"upstream_url", m.UpstreamURL},
		{"upstream_sha256", m.UpstreamSHA256},
		{"license", m.License},
		{"notice", m.Notice},
		{"converter_version", m.ConverterVersion},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("manifest is missing %s", field.name)
		}
	}
	if m.Counts.Concepts < 0 || m.Counts.Lemmas < 0 || m.Counts.Edges < 0 {
		return fmt.Errorf("manifest counts must not be negative")
	}
	return nil
}

// Install loads one artifact into the store inside a single transaction: the source's previous rows are
// deleted, the manifest's provenance row is written, and the three row-sets are streamed in batches. The
// loaded counts must equal the manifest's, so a truncated or malformed artifact rolls back whole and leaves
// no partial install.
func Install(ctx context.Context, client *database.Client, artifactPath string) error {
	m, err := readManifest(artifactPath)
	if err != nil {
		return fmt.Errorf("read kb artifact %s: %w", artifactPath, err)
	}
	if err := m.validate(); err != nil {
		return fmt.Errorf("install kb artifact %s: %w", artifactPath, err)
	}

	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin kb install transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	q := database.New(tx)
	for _, remove := range []func(context.Context, string) error{
		q.DeleteKBEdges, q.DeleteKBLemmas, q.DeleteKBConcepts, q.DeleteKBSource,
	} {
		if err := remove(ctx, m.Source); err != nil {
			return fmt.Errorf("clear kb source %q: %w", m.Source, err)
		}
	}

	if err := q.InsertKBSource(ctx, database.InsertKBSourceParams{
		Source:           m.Source,
		SourceVersion:    m.SourceVersion,
		License:          m.License,
		Notice:           m.Notice,
		UpstreamUrl:      m.UpstreamURL,
		UpstreamSha256:   m.UpstreamSHA256,
		ConverterVersion: m.ConverterVersion,
		ConceptCount:     m.Counts.Concepts,
		LemmaCount:       m.Counts.Lemmas,
		EdgeCount:        m.Counts.Edges,
	}); err != nil {
		return fmt.Errorf("insert kb source %q: %w", m.Source, err)
	}

	loaded, err := loadRows(ctx, q, artifactPath, m.Source)
	if err != nil {
		return fmt.Errorf("load kb artifact %s: %w", artifactPath, err)
	}
	if loaded != m.Counts {
		return fmt.Errorf("kb artifact %s counts mismatch: manifest %+v, loaded %+v", artifactPath, m.Counts, loaded)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit kb install: %w", err)
	}
	return nil
}

// readManifest extracts the manifest entry without reading the row-sets.
func readManifest(artifactPath string) (manifest, error) {
	f, err := os.Open(artifactPath)
	if err != nil {
		return manifest{}, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return manifest{}, fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return manifest{}, fmt.Errorf("artifact has no %s entry", artifactManifest)
		}
		if err != nil {
			return manifest{}, fmt.Errorf("read tar entry: %w", err)
		}
		if path.Clean(hdr.Name) != artifactManifest {
			continue
		}

		var m manifest
		if err := json.NewDecoder(tr).Decode(&m); err != nil {
			return manifest{}, fmt.Errorf("decode %s: %w", artifactManifest, err)
		}
		return m, nil
	}
}

// loadRows streams the three row-set entries into the store and returns what it actually loaded. Every
// required entry must appear exactly once; blank lines are skipped, a malformed row fails the whole install.
func loadRows(ctx context.Context, q *database.Queries, artifactPath, source string) (manifestCounts, error) {
	f, err := os.Open(artifactPath)
	if err != nil {
		return manifestCounts{}, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return manifestCounts{}, fmt.Errorf("open gzip stream: %w", err)
	}
	defer gz.Close()

	var counts manifestCounts
	seen := make(map[string]struct{}, 3)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return counts, fmt.Errorf("read tar entry: %w", err)
		}

		name := path.Clean(hdr.Name)
		switch name {
		case artifactConcepts, artifactLemmas, artifactEdges:
			if _, ok := seen[name]; ok {
				return counts, fmt.Errorf("artifact repeats the %s entry", name)
			}
			seen[name] = struct{}{}
		default:
			continue
		}

		var n int64
		switch name {
		case artifactConcepts:
			n, err = loadConcepts(ctx, q, source, tr)
			counts.Concepts = n
		case artifactLemmas:
			n, err = loadLemmas(ctx, q, source, tr)
			counts.Lemmas = n
		case artifactEdges:
			n, err = loadEdges(ctx, q, source, tr)
			counts.Edges = n
		}
		if err != nil {
			return counts, fmt.Errorf("%s: %w", name, err)
		}
	}

	for _, name := range []string{artifactConcepts, artifactLemmas, artifactEdges} {
		if _, ok := seen[name]; !ok {
			return counts, fmt.Errorf("artifact has no %s entry", name)
		}
	}
	return counts, nil
}

// conceptRecord, lemmaRecord and edgeRecord are the insert payloads; empty optional fields travel as JSON
// null so the nullable columns stay NULL instead of an empty string.
type conceptRecord struct {
	ConceptKey string  `json:"concept_key"`
	Pos        *string `json:"pos"`
	Gloss      *string `json:"gloss"`
}

type lemmaRecord struct {
	ConceptKey string `json:"concept_key"`
	LemmaNorm  string `json:"lemma_norm"`
}

type edgeRecord struct {
	FromConceptKey string   `json:"from_concept_key"`
	ToConceptKey   string   `json:"to_concept_key"`
	Family         string   `json:"family"`
	SourceReltype  string   `json:"source_reltype"`
	Weight         *float64 `json:"weight"`
}

func loadConcepts(ctx context.Context, q *database.Queries, source string, r io.Reader) (int64, error) {
	var count int64
	batch := make([]conceptRecord, 0, insertBatchSize)
	err := scanTSV(r, 3, func(_ int, fields []string) error {
		if fields[0] == "" {
			return fmt.Errorf("empty concept key")
		}
		batch = append(batch, conceptRecord{
			ConceptKey: fields[0],
			Pos:        optionalString(fields[1]),
			Gloss:      optionalString(fields[2]),
		})
		count++
		if len(batch) == insertBatchSize {
			if err := flushRecords(ctx, batch, func(ctx context.Context, records json.RawMessage) error {
				return q.InsertKBConcepts(ctx, database.InsertKBConceptsParams{Source: source, Records: records})
			}); err != nil {
				return err
			}
			batch = batch[:0]
		}
		return nil
	})
	if err != nil {
		return count, err
	}
	return count, flushRecords(ctx, batch, func(ctx context.Context, records json.RawMessage) error {
		return q.InsertKBConcepts(ctx, database.InsertKBConceptsParams{Source: source, Records: records})
	})
}

func loadLemmas(ctx context.Context, q *database.Queries, source string, r io.Reader) (int64, error) {
	var count int64
	batch := make([]lemmaRecord, 0, insertBatchSize)
	err := scanTSV(r, 2, func(_ int, fields []string) error {
		if fields[0] == "" || fields[1] == "" {
			return fmt.Errorf("empty concept key or lemma")
		}
		batch = append(batch, lemmaRecord{ConceptKey: fields[0], LemmaNorm: fields[1]})
		count++
		if len(batch) == insertBatchSize {
			if err := flushRecords(ctx, batch, func(ctx context.Context, records json.RawMessage) error {
				return q.InsertKBLemmas(ctx, database.InsertKBLemmasParams{Source: source, Records: records})
			}); err != nil {
				return err
			}
			batch = batch[:0]
		}
		return nil
	})
	if err != nil {
		return count, err
	}
	return count, flushRecords(ctx, batch, func(ctx context.Context, records json.RawMessage) error {
		return q.InsertKBLemmas(ctx, database.InsertKBLemmasParams{Source: source, Records: records})
	})
}

func loadEdges(ctx context.Context, q *database.Queries, source string, r io.Reader) (int64, error) {
	var count int64
	batch := make([]edgeRecord, 0, insertBatchSize)
	err := scanTSV(r, 5, func(_ int, fields []string) error {
		if fields[0] == "" || fields[1] == "" {
			return fmt.Errorf("empty edge endpoint")
		}
		if _, ok := validFamilies[Family(fields[2])]; !ok {
			return fmt.Errorf("unknown family %q", fields[2])
		}
		if fields[3] == "" {
			return fmt.Errorf("empty source reltype")
		}

		weight, err := parseWeight(fields[4])
		if err != nil {
			return err
		}
		batch = append(batch, edgeRecord{
			FromConceptKey: fields[0],
			ToConceptKey:   fields[1],
			Family:         fields[2],
			SourceReltype:  fields[3],
			Weight:         weight,
		})
		count++
		if len(batch) == insertBatchSize {
			if err := flushRecords(ctx, batch, func(ctx context.Context, records json.RawMessage) error {
				return q.InsertKBEdges(ctx, database.InsertKBEdgesParams{Source: source, Records: records})
			}); err != nil {
				return err
			}
			batch = batch[:0]
		}
		return nil
	})
	if err != nil {
		return count, err
	}
	return count, flushRecords(ctx, batch, func(ctx context.Context, records json.RawMessage) error {
		return q.InsertKBEdges(ctx, database.InsertKBEdgesParams{Source: source, Records: records})
	})
}

// scanTSV walks a tab-separated stream with an exact column count per line. Blank lines are skipped; a
// malformed line is reported with its number so an artifact can be fixed at the source.
func scanTSV(r io.Reader, columns int, fn func(line int, fields []string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		fields := strings.Split(text, "\t")
		if len(fields) != columns {
			return fmt.Errorf("line %d: %d columns, want %d", line, len(fields), columns)
		}
		if err := fn(line, fields); err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read tsv: %w", err)
	}
	return nil
}

// flushRecords marshals and inserts one batch; an empty batch is a no-op.
func flushRecords[T any](ctx context.Context, batch []T, insert func(context.Context, json.RawMessage) error) error {
	if len(batch) == 0 {
		return nil
	}
	records, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal rows: %w", err)
	}
	if err := insert(ctx, records); err != nil {
		return fmt.Errorf("insert rows: %w", err)
	}
	return nil
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func parseWeight(s string) (*float64, error) {
	if s == "" {
		return nil, nil
	}
	weight, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid weight %q", s)
	}
	if math.IsNaN(weight) || math.IsInf(weight, 0) {
		return nil, fmt.Errorf("invalid weight %q", s)
	}
	return &weight, nil
}
