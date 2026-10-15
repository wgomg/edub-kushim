package tags

import (
	"context"
	"database/sql"
	"errors"
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

// ReconstructibilityBatch reads how far each compound is its parts' product over the live store, one reading
// per cover keyed by the compound as given. Names resolve through the store's own recipe (StoredName), and
// covers naming one store compound are measured once and share the reading, so a pair of spellings that
// house-form alike neither doubles the intersection counts nor disposes two ways. The live masses and the
// parts' intersections come from two batched queries, so the cost is three round trips per batch rather than
// per cover. A cover the store cannot measure — a name it does not hold, or a thin or empty extension —
// yields no reading, which callers treat as no evidence and answer from their own fallback.
func (s *Store) ReconstructibilityBatch(ctx context.Context, compounds []string, parts [][]string) (map[string]ReconstructibilityReading, error) {
	readings := make(map[string]ReconstructibilityReading, len(compounds))
	names := make([]string, 0, len(compounds))
	seen := make(map[string]bool, len(compounds))
	for i, compound := range compounds {
		key := StoredName(compound)
		if !seen[key] {
			seen[key] = true
			names = append(names, key)
		}
		for _, part := range parts[i] {
			key := StoredName(part)
			if !seen[key] {
				seen[key] = true
				names = append(names, key)
			}
		}
	}
	if len(names) == 0 {
		return readings, nil
	}
	rows, err := s.q.ListTagIDsByNames(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("load reconstructibility names: %w", err)
	}
	ids := make(map[string]int64, len(rows))
	for _, row := range rows {
		ids[row.Name] = row.ID
	}

	measured := make(map[int64]bool, len(compounds))
	massIDs := make([]int64, 0, len(compounds))
	compoundIDs := make([]int64, 0, len(compounds))
	partIDs := make([]int64, 0, len(compounds))
	for i, compound := range compounds {
		id, ok := ids[StoredName(compound)]
		if !ok || len(parts[i]) == 0 || measured[id] {
			continue
		}
		pairs := make([]int64, 0, len(parts[i]))
		for _, part := range parts[i] {
			partID, ok := ids[StoredName(part)]
			if !ok {
				pairs = nil
				break
			}
			pairs = append(pairs, partID)
		}
		if pairs == nil {
			continue
		}
		measured[id] = true
		massIDs = append(massIDs, id)
		for _, partID := range pairs {
			compoundIDs = append(compoundIDs, id)
			partIDs = append(partIDs, partID)
		}
	}
	mass, err := s.LiveMass(ctx, massIDs)
	if err != nil {
		return nil, err
	}
	intersections, err := s.CompoundIntersections(ctx, compoundIDs, partIDs)
	if err != nil {
		return nil, err
	}
	for i, compound := range compounds {
		id, ok := ids[StoredName(compound)]
		if !ok || !measured[id] {
			continue
		}
		intersection := intersections[id]
		readings[compound] = NewReconstructibilityReading(compound, parts[i], mass[id],
			intersection.PartsIntersection, intersection.Both)
	}
	return readings, nil
}

// SiblingWorklist reads the latest measurement run's containment worklist: only the overlap records are
// loaded and only the containment worklist is kept, so the cost tracks the run's co-occurring pairs instead
// of every operator and worklist. The snapshot's other worklists stay empty.
func (s *Store) SiblingWorklist(ctx context.Context) (*WorklistSnapshot, error) {
	run, err := s.q.LatestTagWorklistRun(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &WorklistSnapshot{Absent: true}, nil
		}
		return nil, fmt.Errorf("latest tag worklist run: %w", err)
	}
	rows, err := s.q.TagMeasurementRecordsForRun(ctx, database.TagMeasurementRecordsForRunParams{
		RunID:    run.RunID,
		Operator: sql.NullString{String: string(OperatorOverlap), Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("tag overlap records: %w", err)
	}
	records, err := DecodeRows(rows)
	if err != nil {
		return nil, err
	}
	// the query carried only overlap records, so every row feeds the pair cut; only the containment worklist
	// is kept here
	var derived Worklists
	for _, record := range records {
		derived.addOverlap(record)
	}
	return &WorklistSnapshot{
		RunID:      run.RunID,
		SnapshotAt: run.SnapshotAt,
		Worklists:  Worklists{Siblings: derived.Siblings},
	}, nil
}

// WorklistSnapshot is one measurement run's derived worklists. Absent separates "no run yet" from "a run
// with empty worklists" — both leave Worklists empty, so only Absent marks zero-data operation.
type WorklistSnapshot struct {
	RunID      uuid.UUID
	SnapshotAt time.Time
	Absent     bool
	Worklists  Worklists
}

// LatestWorklists derives the review worklists from the latest measurement run under the current cuts. The
// cuts are consumer-side (extension-algebra §4), so a rule-set bump re-derives an old run's raw numbers
// instead of invalidating them. Only runs that produced measurement operators count — the completeness
// audit writes coverage-only runs into the shared substrate, and those never become the worklist run. A
// store with no run returns an absent snapshot and no error: zero data is a normal state, not a failure.
func (s *Store) LatestWorklists(ctx context.Context) (*WorklistSnapshot, error) {
	run, err := s.q.LatestTagWorklistRun(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &WorklistSnapshot{Absent: true}, nil
		}
		return nil, fmt.Errorf("latest tag worklist run: %w", err)
	}
	rows, err := s.q.TagMeasurementRecordsForRun(ctx, database.TagMeasurementRecordsForRunParams{RunID: run.RunID})
	if err != nil {
		return nil, fmt.Errorf("tag measurement records: %w", err)
	}
	records, err := DecodeRows(rows)
	if err != nil {
		return nil, err
	}
	return &WorklistSnapshot{
		RunID:      run.RunID,
		SnapshotAt: run.SnapshotAt,
		Worklists:  DeriveWorklists(records),
	}, nil
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
