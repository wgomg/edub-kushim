// Package kb is the offline lexical knowledge base: it installs the published source artifacts into
// Postgres and exposes the read interfaces over the source-scoped row-sets. Every row is keyed by its
// source and no cross-source concept identity exists; installing a source replaces exactly its own rows,
// and an absent source is indistinguishable from a miss — an empty result is never negative evidence. The
// package is pure Go (no cgo) so it links into edub. The artifact format is the contract between the
// converter script and the installer: the manifest carries provenance, license and counts, and the installer
// refuses an artifact whose manifest lacks the license, the notice, or the pinned identity.
package kb

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
)

// Family is the pinned relation taxonomy shared by every source. It is deliberately small: each source's
// native relation types map onto it, and the native name is carried beside it on the edge.
type Family string

// The families an edge can carry.
const (
	FamilyExact    Family = "exact"
	FamilySimilar  Family = "similar"
	FamilyBroader  Family = "broader"
	FamilyNarrower Family = "narrower"
	FamilyInstance Family = "instance"
	FamilyPart     Family = "part"
	FamilyAntonym  Family = "antonym"
	FamilyDistinct Family = "distinct"
)

// validFamilies is the installer's local copy of the schema's family check, so a malformed artifact fails
// with a row location instead of a constraint violation.
var validFamilies = map[Family]struct{}{
	FamilyExact:    {},
	FamilySimilar:  {},
	FamilyBroader:  {},
	FamilyNarrower: {},
	FamilyInstance: {},
	FamilyPart:     {},
	FamilyAntonym:  {},
	FamilyDistinct: {},
}

// Source is one installed artifact's provenance and row counts, as the capability report exposes it.
type Source struct {
	Name             string
	Version          string
	License          string
	Notice           string
	UpstreamURL      string
	UpstreamSHA256   string
	ConverterVersion string
	Concepts         int64
	Lemmas           int64
	Edges            int64
	InstalledAt      time.Time
}

// Anchor is one concept a label resolves to: the source-scoped concept key plus the sense metadata the
// source records for it. Pos and Gloss are empty when the source has no such data (ConceptNet nodes are
// unsensed).
type Anchor struct {
	Source     string
	ConceptKey string
	Pos        string
	Gloss      string
}

// TypedEdge is one directed relation of one source, projected onto the lemma strings both concepts carry.
// Weight is nil when the source records no weights. RelType is the source's native relation name; derived
// edges (the co-lemma exact expansion) carry the synthetic name "co-lemma".
type TypedEdge struct {
	Source        string
	SourceVersion string
	Family        Family
	RelType       string
	Weight        *float64
	FromKey       string
	ToKey         string
	FromLemmas    []string
	ToLemmas      []string
}

// coLemmaRelType names the synthetic exact edges derived from two lemmas sharing one concept. It is not an
// upstream relation name and exists so a consumer can tell a derived edge from a materialized one.
const coLemmaRelType = "co-lemma"

// lemmaForm is the store's house form for lemma lookups: lowercase, underscores as spaces, collapsed
// whitespace. The converter writes every lemma_norm through the same form, so a caller normalizes the
// label once and compares directly.
func lemmaForm(label string) string {
	s := strings.ToLower(strings.TrimSpace(label))
	s = strings.ReplaceAll(s, "_", " ")
	return strings.Join(strings.Fields(s), " ")
}

// Store is the offline knowledge base over one database client and one artifact cache, and it owns the
// published artifact pins: callers name a configured source, and the store resolves the pin, compares
// the installed version, downloads and verifies the artifact when needed, installs it, and answers reads
// from the installed rows.
type Store struct {
	client *database.Client
	dir    string
}

// NewStore prepares the store for a database client and the application's config directory; the downloaded
// artifacts live under <configDir>/kb.
func NewStore(client *database.Client, configDir string) *Store {
	return &Store{client: client, dir: filepath.Join(configDir, "kb")}
}

// Sources reports the installed sources with their versions, licenses and row counts. A source that is not
// installed is simply absent from the list.
func (s *Store) Sources(ctx context.Context) ([]Source, error) {
	rows, err := s.client.ListKBSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list kb sources: %w", err)
	}

	sources := make([]Source, 0, len(rows))
	for _, r := range rows {
		sources = append(sources, Source{
			Name:             r.Source,
			Version:          r.SourceVersion,
			License:          r.License,
			Notice:           r.Notice,
			UpstreamURL:      r.UpstreamUrl,
			UpstreamSHA256:   r.UpstreamSha256,
			ConverterVersion: r.ConverterVersion,
			Concepts:         r.ConceptCount,
			Lemmas:           r.LemmaCount,
			Edges:            r.EdgeCount,
			InstalledAt:      r.InstalledAt,
		})
	}
	return sources, nil
}
