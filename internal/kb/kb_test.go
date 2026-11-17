package kb

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wgomg/edub-kushim/internal/database"
)

type fixtureManifest struct {
	Source           string `json:"source"`
	SourceVersion    string `json:"source_version"`
	UpstreamURL      string `json:"upstream_url"`
	UpstreamSHA256   string `json:"upstream_sha256"`
	License          string `json:"license"`
	Notice           string `json:"notice"`
	ConverterVersion string `json:"converter_version"`
	Filters          string `json:"filters"`
	Counts           struct {
		Concepts int64 `json:"concepts"`
		Lemmas   int64 `json:"lemmas"`
		Edges    int64 `json:"edges"`
	} `json:"counts"`
	GeneratedAt string `json:"generated_at"`
}

type fixtureRows struct {
	concepts [][3]string
	lemmas   [][2]string
	edges    [][5]string
}

func fixtureManifestFor(source string, rows fixtureRows) fixtureManifest {
	var m fixtureManifest
	m.Source = source
	m.SourceVersion = "test-1"
	m.UpstreamURL = "https://example.com/" + source
	m.UpstreamSHA256 = strings.Repeat("0", 64)
	m.License = "CC0-1.0"
	m.Notice = "fixture notice"
	m.ConverterVersion = "9"
	m.Filters = "fixture filters"
	m.Counts.Concepts = int64(len(rows.concepts))
	m.Counts.Lemmas = int64(len(rows.lemmas))
	m.Counts.Edges = int64(len(rows.edges))
	m.GeneratedAt = "2026-11-16T00:00:00Z"
	return m
}

func fixtureOEWNRows() fixtureRows {
	return fixtureRows{
		concepts: [][3]string{
			{"ewn-car-n", "n", "a motor vehicle"},
			{"ewn-auto-n", "n", "a motor vehicle, an automobile"},
			{"ewn-wheel-n", "n", "a circular component"},
		},
		lemmas: [][2]string{
			{"ewn-car-n", "car"},
			{"ewn-car-n", "automobile"},
			{"ewn-auto-n", "car"},
			{"ewn-auto-n", "motorcar"},
			{"ewn-wheel-n", "wheel"},
		},
		edges: [][5]string{
			{"ewn-wheel-n", "ewn-car-n", "part", "mero_part", ""},
			{"ewn-car-n", "ewn-wheel-n", "antonym", "antonym", ""},
		},
	}
}

func fixtureConceptNetRows() fixtureRows {
	return fixtureRows{
		concepts: [][3]string{
			{"/c/en/dog", "", ""},
			{"/c/en/puppy", "", ""},
		},
		lemmas: [][2]string{
			{"/c/en/dog", "dog"},
			{"/c/en/puppy", "puppy"},
		},
		edges: [][5]string{
			{"/c/en/dog", "/c/en/puppy", "exact", "/r/Synonym", "2.5"},
		},
	}
}

func writeFixtureArtifact(t *testing.T, manifest fixtureManifest, rows fixtureRows) string {
	t.Helper()

	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal fixture manifest: %v", err)
	}

	path := filepath.Join(t.TempDir(), manifest.Source+".tar.gz")
	raw, err := os.Create(path)
	if err != nil {
		t.Fatalf("create fixture artifact: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			raw.Close()
		}
	}()

	gz := gzip.NewWriter(raw)
	tw := tar.NewWriter(gz)
	addEntry := func(name, body string) {
		t.Helper()
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatalf("write tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("write tar entry %s: %v", name, err)
		}
	}
	addEntry("manifest.json", string(payload)+"\n")

	join := func(fields ...string) string { return strings.Join(fields, "\t") + "\n" }
	var concepts, lemmas, edges strings.Builder
	for _, r := range rows.concepts {
		concepts.WriteString(join(r[0], r[1], r[2]))
	}
	for _, r := range rows.lemmas {
		lemmas.WriteString(join(r[0], r[1]))
	}
	for _, r := range rows.edges {
		edges.WriteString(join(r[0], r[1], r[2], r[3], r[4]))
	}
	addEntry("concepts.tsv", concepts.String())
	addEntry("lemmas.tsv", lemmas.String())
	addEntry("edges.tsv", edges.String())

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close fixture artifact: %v", err)
	}
	closed = true
	return path
}

func setupTestStore(t *testing.T) (*database.Client, *Store) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	return client, NewStore(client, t.TempDir(), []string{"kbtest", "cnet"})
}

func mustCountSource(t *testing.T, store *Store, source string, version string) Source {
	t.Helper()
	sources, err := store.Sources(context.Background())
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	for _, s := range sources {
		if s.Name == source && s.Version == version {
			return s
		}
	}
	t.Fatalf("source %s version %s not installed", source, version)
	return Source{}
}

func TestInstallHappyAndReads(t *testing.T) {
	client, store := setupTestStore(t)
	ctx := context.Background()
	rows := fixtureOEWNRows()
	path := writeFixtureArtifact(t, fixtureManifestFor("kbtest", rows), rows)
	if err := Install(ctx, client, path); err != nil {
		t.Fatalf("install: %v", err)
	}

	got := mustCountSource(t, store, "kbtest", "test-1")
	if got.Concepts != 3 || got.Lemmas != 5 || got.Edges != 2 {
		t.Fatalf("counts = %d/%d/%d, want 3/5/2", got.Concepts, got.Lemmas, got.Edges)
	}
	if got.License != "CC0-1.0" || got.Notice != "fixture notice" {
		t.Fatalf("provenance = %q/%q", got.License, got.Notice)
	}

	anchors, err := store.Resolve(ctx, " Car ")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(anchors) != 2 {
		t.Fatalf("resolve car = %d anchors, want 2", len(anchors))
	}
	if anchors[0].ConceptKey != "ewn-auto-n" || anchors[1].ConceptKey != "ewn-car-n" {
		t.Fatalf("resolve order = %v, want [ewn-auto-n ewn-car-n]", anchors)
	}
	if anchors[0].Pos != "n" || anchors[0].Gloss == "" {
		t.Fatalf("anchor metadata = pos %q gloss %q", anchors[0].Pos, anchors[0].Gloss)
	}
	single, err := store.Resolve(ctx, "motorcar")
	if err != nil {
		t.Fatalf("resolve motorcar: %v", err)
	}
	if len(single) != 1 || single[0].ConceptKey != "ewn-auto-n" {
		t.Fatalf("resolve motorcar = %+v, want [ewn-auto-n]", single)
	}

	missing, err := store.Resolve(ctx, "zeppelin")
	if err != nil {
		t.Fatalf("resolve miss: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("resolve miss = %d anchors, want empty", len(missing))
	}
}

func TestInstallRefusesUnlicensedManifest(t *testing.T) {
	client, _ := setupTestStore(t)
	for _, tc := range []struct {
		name   string
		modify func(*fixtureManifest)
	}{
		{"missing license", func(m *fixtureManifest) { m.License = "" }},
		{"missing notice", func(m *fixtureManifest) { m.Notice = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := fixtureOEWNRows()
			manifest := fixtureManifestFor("kbtest", rows)
			manifest.Counts.Concepts = int64(len(rows.concepts))
			tc.modify(&manifest)
			path := writeFixtureArtifact(t, manifest, rows)
			if err := Install(context.Background(), client, path); err == nil {
				t.Fatal("expected install error")
			}
		})
	}
}

func TestInstallRejectsCountMismatch(t *testing.T) {
	client, store := setupTestStore(t)
	rows := fixtureOEWNRows()
	manifest := fixtureManifestFor("kbtest", rows)
	manifest.Counts.Edges = int64(len(rows.edges)) + 1
	path := writeFixtureArtifact(t, manifest, rows)
	if err := Install(context.Background(), client, path); err == nil {
		t.Fatal("expected count mismatch error")
	}
	sources, err := store.Sources(context.Background())
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("rolled-back install left %d sources", len(sources))
	}
}

func TestInstallRejectsBadFamily(t *testing.T) {
	client, _ := setupTestStore(t)
	rows := fixtureOEWNRows()
	rows.edges = append(rows.edges, [5]string{"ewn-car-n", "ewn-wheel-n", "related", "related", ""})
	path := writeFixtureArtifact(t, fixtureManifestFor("kbtest", rows), rows)
	err := Install(context.Background(), client, path)
	if err == nil || !strings.Contains(err.Error(), `unknown family "related"`) {
		t.Fatalf("expected unknown-family error, got %v", err)
	}
}

func TestReinstallReplacesSource(t *testing.T) {
	client, store := setupTestStore(t)
	ctx := context.Background()

	rowsV1 := fixtureOEWNRows()
	if err := Install(ctx, client, writeFixtureArtifact(t, fixtureManifestFor("kbtest", rowsV1), rowsV1)); err != nil {
		t.Fatalf("install v1: %v", err)
	}

	rowsV2 := fixtureRows{
		concepts: [][3]string{{"ewn-bike-n", "n", "a two-wheeled vehicle"}},
		lemmas:   [][2]string{{"ewn-bike-n", "bike"}},
		edges:    [][5]string{},
	}
	manifest := fixtureManifestFor("kbtest", rowsV2)
	manifest.SourceVersion = "test-2"
	if err := Install(ctx, client, writeFixtureArtifact(t, manifest, rowsV2)); err != nil {
		t.Fatalf("install v2: %v", err)
	}

	got := mustCountSource(t, store, "kbtest", "test-2")
	if got.Concepts != 1 || got.Edges != 0 {
		t.Fatalf("replaced counts = %d/%d/%d", got.Concepts, got.Lemmas, got.Edges)
	}
	if anchors, err := store.Resolve(ctx, "car"); err != nil || len(anchors) != 0 {
		t.Fatalf("stale resolve after replace = %v, %v", anchors, err)
	}
	if anchors, err := store.Resolve(ctx, "bike"); err != nil || len(anchors) != 1 {
		t.Fatalf("replaced resolve = %v, %v", anchors, err)
	}
}

func TestNeighboursDerivesCoLemmaExact(t *testing.T) {
	client, store := setupTestStore(t)
	ctx := context.Background()
	rows := fixtureOEWNRows()
	if err := Install(ctx, client, writeFixtureArtifact(t, fixtureManifestFor("kbtest", rows), rows)); err != nil {
		t.Fatalf("install: %v", err)
	}

	edges, err := store.Neighbours(ctx, "automobile", []Family{FamilyExact, FamilyPart, FamilyAntonym})
	if err != nil {
		t.Fatalf("neighbours: %v", err)
	}
	var derived, materialized int
	for _, e := range edges {
		switch {
		case e.Family == FamilyExact && e.RelType == "co-lemma" && e.Source == "kbtest":
			derived++
			if e.SourceVersion != "test-1" {
				t.Fatalf("derived edge version = %q", e.SourceVersion)
			}
			if len(e.FromLemmas) != 1 || e.FromLemmas[0] != "automobile" {
				t.Fatalf("derived edge from = %v", e.FromLemmas)
			}
			if len(e.ToLemmas) != 1 || e.ToLemmas[0] == "automobile" {
				t.Fatalf("derived edge to = %v", e.ToLemmas)
			}
		case e.Family == FamilyAntonym:
			materialized++
			if e.Weight != nil {
				t.Fatalf("oewn edge weight should be nil")
			}
		}
	}
	if derived == 0 {
		t.Fatalf("no derived co-lemma exact edges in %v", edges)
	}
	if materialized == 0 {
		t.Fatalf("no materialized edges in %v", edges)
	}

	again, err := store.Neighbours(ctx, "automobile", []Family{FamilyPart})
	if err != nil {
		t.Fatalf("neighbours families: %v", err)
	}
	for _, e := range again {
		if e.Family == FamilyExact {
			t.Fatalf("exact edge returned without exact family: %+v", e)
		}
	}
}

func TestNeighboursProjectsConceptNetWeight(t *testing.T) {
	client, store := setupTestStore(t)
	ctx := context.Background()
	rows := fixtureConceptNetRows()
	if err := Install(ctx, client, writeFixtureArtifact(t, fixtureManifestFor("cnet", rows), rows)); err != nil {
		t.Fatalf("install: %v", err)
	}

	edges, err := store.Neighbours(ctx, "dog", []Family{FamilyExact})
	if err != nil {
		t.Fatalf("neighbours: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("neighbours = %+v, want 1 exact edge", edges)
	}
	e := edges[0]
	if e.RelType != "/r/Synonym" || e.Weight == nil || *e.Weight != 2.5 {
		t.Fatalf("edge = %+v, want /r/Synonym weight 2.5", e)
	}
	if len(e.ToLemmas) != 1 || e.ToLemmas[0] != "puppy" {
		t.Fatalf("target lemmas = %v, want [puppy]", e.ToLemmas)
	}
}

func TestPairProbeBothDirectionsAndSharedConcept(t *testing.T) {
	client, store := setupTestStore(t)
	ctx := context.Background()
	rows := fixtureOEWNRows()
	if err := Install(ctx, client, writeFixtureArtifact(t, fixtureManifestFor("kbtest", rows), rows)); err != nil {
		t.Fatalf("install: %v", err)
	}

	edges, err := store.PairProbe(ctx, "automobile", "wheel", []Family{FamilyPart, FamilyAntonym, FamilyExact})
	if err != nil {
		t.Fatalf("pair probe: %v", err)
	}
	var fromAuto, fromWheel int
	for _, e := range edges {
		switch {
		case e.FromLemmas[0] == "automobile" && e.ToLemmas[0] == "wheel":
			fromAuto++
			if e.Family != FamilyAntonym {
				t.Fatalf("auto→wheel family = %s", e.Family)
			}
		case e.FromLemmas[0] == "wheel" && e.ToLemmas[0] == "automobile":
			fromWheel++
		default:
			t.Fatalf("unexpected pair edge: %+v", e)
		}
	}
	if fromAuto == 0 || fromWheel == 0 {
		t.Fatalf("pair edges = %+v, want both directions", edges)
	}

	shared, err := store.PairProbe(ctx, "car", "automobile", []Family{FamilyExact})
	if err != nil {
		t.Fatalf("shared-concept probe: %v", err)
	}
	if len(shared) != 1 {
		t.Fatalf("shared-concept edges = %+v, want 1", shared)
	}
	if shared[0].Family != FamilyExact || shared[0].RelType != "co-lemma" {
		t.Fatalf("shared-concept edge = %+v", shared[0])
	}
	if len(shared[0].FromLemmas) != 1 || shared[0].FromLemmas[0] != "car" ||
		len(shared[0].ToLemmas) != 1 || shared[0].ToLemmas[0] != "automobile" {
		t.Fatalf("shared-concept lemmas = %v -> %v", shared[0].FromLemmas, shared[0].ToLemmas)
	}

	unknown, err := store.PairProbe(ctx, "automobile", "gazebo56", []Family{FamilyExact})
	if err != nil {
		t.Fatalf("unknown probe: %v", err)
	}
	if len(unknown) != 0 {
		t.Fatalf("unknown probe = %+v, want empty", unknown)
	}

	if _, err := store.Neighbours(ctx, "car", []Family{"not-a-family"}); err == nil {
		t.Fatal("expected unknown-family error")
	}
}

func TestEnsureRejectsUnknownSource(t *testing.T) {
	_, store := setupTestStore(t)
	if err := store.Ensure(context.Background(), "no-such-source"); err == nil {
		t.Fatal("expected unknown-source error")
	}
}

func TestMissingReportsUnknownSources(t *testing.T) {
	client, store := setupTestStore(t)
	ctx := context.Background()
	rows := fixtureOEWNRows()
	if err := Install(ctx, client, writeFixtureArtifact(t, fixtureManifestFor("kbtest", rows), rows)); err != nil {
		t.Fatalf("install: %v", err)
	}

	missing, err := store.Missing(ctx, []string{"kbtest", "no-such-source"})
	if err != nil {
		t.Fatalf("missing: %v", err)
	}
	if len(missing) != 2 {
		t.Fatalf("missing = %v, want [kbtest no-such-source]", missing)
	}
}
