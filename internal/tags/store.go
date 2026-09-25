package tags

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
)

// Store reads the tag vocabulary for measurement sweeps and persists their
// readings: live masses, pair evidence stats, the sweep snapshot, compound
// intersections, and record insertion. All queries run over the given
// queries value, which the caller attaches to its own transaction when one
// is in flight.
type Store struct {
	q *database.Queries
}

// NewStore builds the store over the given queries.
func NewStore(q *database.Queries) *Store { return &Store{q: q} }

// LiveMass returns the live document count per tag id; empty input
// returns an empty map.
func (s *Store) LiveMass(ctx context.Context, tagIDs []int64) (map[int64]int64, error) {
	if len(tagIDs) == 0 {
		return map[int64]int64{}, nil
	}
	rows, err := s.q.CountLiveTagDocumentsByTagIDs(ctx, tagIDs)
	if err != nil {
		return nil, fmt.Errorf("count live tag documents: %w", err)
	}
	mass := make(map[int64]int64, len(rows))
	for _, row := range rows {
		mass[row.TagID] = row.DocumentCount
	}
	return mass, nil
}

// PairNames names one query/target pair for the evidence-stats lookup.
type PairNames struct {
	Query, Target string
}

// PairVerdictStats aggregates the verdict events recorded for the given
// pairs: the documents seen per pair, the verdict events, and the
// same/variant/related counts.
type PairVerdictStats struct {
	Query, Target string
	Documents     int64
	Events        int64
	Same          int64
	Variant       int64
	Related       int64
}

// PairVerdictStats loads the verdict-event evidence for the given
// query/target pairs, in input order.
func (s *Store) PairVerdictStats(ctx context.Context, pairs []PairNames) ([]PairVerdictStats, error) {
	queries := make([]string, 0, len(pairs))
	targets := make([]string, 0, len(pairs))
	for _, p := range pairs {
		queries = append(queries, p.Query)
		targets = append(targets, p.Target)
	}
	rows, err := s.q.TagPairEvidenceStats(ctx, database.TagPairEvidenceStatsParams{
		Queries: queries,
		Targets: targets,
	})
	if err != nil {
		return nil, fmt.Errorf("tag pair evidence stats: %w", err)
	}
	out := make([]PairVerdictStats, 0, len(rows))
	for _, row := range rows {
		out = append(out, PairVerdictStats{
			Query:     row.Query,
			Target:    row.Target,
			Documents: row.DocumentCount,
			Events:    row.EventCount,
			Same:      row.SameCount,
			Variant:   row.VariantCount,
			Related:   row.RelatedCount,
		})
	}
	return out, nil
}

// LoadSnapshot reads the whole tag vocabulary, promoted and provisional,
// with live document masses and the pairwise overlap counts into a
// Snapshot for a measurement sweep.
func (s *Store) LoadSnapshot(ctx context.Context) (*Snapshot, error) {
	rows, err := s.q.ListTagsForMeasure(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	tags := make([]Tag, 0, len(rows))
	for _, row := range rows {
		tags = append(tags, Tag{ID: row.ID, Name: row.Name, Provisional: !row.PromotedAt.Valid})
	}

	massRows, err := s.q.CountLiveTagDocumentsByTagIDs(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("tag live masses: %w", err)
	}
	mass := make(map[int64]int64, len(massRows))
	for _, row := range massRows {
		mass[row.TagID] = row.DocumentCount
	}

	pairRows, err := s.q.TagLivePairOverlaps(ctx)
	if err != nil {
		return nil, fmt.Errorf("tag live pair overlaps: %w", err)
	}
	pairs := make(map[Pair]int64, len(pairRows))
	for _, row := range pairRows {
		pairs[Pair{A: row.TagA, B: row.TagB}] = row.DocumentCount
	}

	return NewSnapshot(tags, mass, pairs), nil
}

// CompoundIntersection holds the two counts a reconstructibility reading
// needs: the intersection across the compound's parts, and the documents
// both the compound and that intersection carry.
type CompoundIntersection struct {
	PartsIntersection int64
	Both              int64
}

// CompoundIntersections computes, per compound id, the parts'
// intersection and the compound-and-parts overlap for the given id lists.
// Empty input returns an empty map.
func (s *Store) CompoundIntersections(ctx context.Context, compoundIDs, partIDs []int64) (map[int64]CompoundIntersection, error) {
	out := make(map[int64]CompoundIntersection, len(compoundIDs))
	if len(compoundIDs) == 0 {
		return out, nil
	}
	rows, err := s.q.TagCompoundReconstructibility(ctx, database.TagCompoundReconstructibilityParams{
		CompoundIds: compoundIDs,
		PartIds:     partIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("tag compound reconstructibility: %w", err)
	}
	for _, row := range rows {
		out[row.CompoundID] = CompoundIntersection{
			PartsIntersection: row.PartsIntersection,
			Both:              row.Both,
		}
	}
	return out, nil
}

const insertChunk = 2000

// InsertRecords persists the sweep's readings in chunks under the given
// run id, snapshot instant and rule set version.
func (s *Store) InsertRecords(ctx context.Context, runID uuid.UUID, snapshotAt time.Time, ruleSetVersion string, records []Record) error {
	for start := 0; start < len(records); start += insertChunk {
		end := min(start+insertChunk, len(records))
		payload, err := EncodeRows(records[start:end])
		if err != nil {
			return fmt.Errorf("encode measurement records: %w", err)
		}
		if err := s.q.InsertTagMeasurements(ctx, database.InsertTagMeasurementsParams{
			RunID:          runID,
			SnapshotAt:     snapshotAt,
			RuleSetVersion: ruleSetVersion,
			Records:        payload,
		}); err != nil {
			return fmt.Errorf("insert measurement records: %w", err)
		}
	}
	return nil
}
