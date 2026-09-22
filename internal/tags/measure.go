package tags

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
)

type Runner struct {
	client *database.Client
	reader *ExtensionReader
}

func NewRunner(client *database.Client, aliases AliasResolver) *Runner {
	return &Runner{
		client: client,
		reader: NewExtensionReader(aliases),
	}
}

const ReportPrefix = "tagmeasure-report: "

type Report struct {
	RunID          string         `json:"run_id"`
	SnapshotAt     time.Time      `json:"snapshot_at"`
	RuleSetVersion string         `json:"rule_set_version"`
	Tags           int            `json:"tags"`
	Operators      map[string]int `json:"operators"`
	States         map[string]int `json:"states"`
	Skipped        map[string]int `json:"skipped"`
	Worklists      WorklistCounts `json:"worklists"`
}

type WorklistCounts struct {
	Twins          int `json:"twins"`
	TwinsGray      int `json:"twins_gray"`
	CompoundSplits int `json:"compound_splits"`
	CompoundsGray  int `json:"compounds_gray"`
	Siblings       int `json:"siblings"`
	SiblingsGray   int `json:"siblings_gray"`
}

func (r *Runner) Run(ctx context.Context, runID uuid.UUID) (*Report, error) {
	tx, err := r.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("begin tag measure transaction: %w", err)
	}
	defer tx.Rollback()

	q := r.client.Queries.WithTx(tx)
	store := NewStore(q)

	snapshotAt, err := q.SnapshotNow(ctx)
	if err != nil {
		return nil, fmt.Errorf("read snapshot instant: %w", err)
	}
	snap, err := store.LoadSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	report := &Report{
		SnapshotAt:     snapshotAt,
		RuleSetVersion: RuleSetVersion,
		Tags:           len(snap.Tags),
		Operators:      map[string]int{},
		States:         map[string]int{},
		Skipped:        map[string]int{},
	}

	records, err := r.build(ctx, store, snap, report)
	if err != nil {
		return nil, err
	}

	worklists := DeriveWorklists(records)
	for _, t := range worklists.Twins {
		if t.Gray {
			report.Worklists.TwinsGray++
		} else {
			report.Worklists.Twins++
		}
	}
	for _, c := range worklists.CompoundSplits {
		if c.Gray {
			report.Worklists.CompoundsGray++
		} else {
			report.Worklists.CompoundSplits++
		}
	}
	for _, s := range worklists.Siblings {
		if s.Gray {
			report.Worklists.SiblingsGray++
		} else {
			report.Worklists.Siblings++
		}
	}

	if err := store.InsertRecords(ctx, runID, snapshotAt, RuleSetVersion, records); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tag measure transaction: %w", err)
	}

	report.RunID = runID.String()
	return report, nil
}

func (r *Runner) build(ctx context.Context, store *Store, snap *Snapshot, report *Report) ([]Record, error) {
	records := make([]Record, 0, len(snap.Tags)+len(snap.Pairs))

	seenLabel := make(map[string]bool, len(snap.Tags))
	for _, t := range snap.Tags {
		label := r.reader.Canonical(t.Name)
		if seenLabel[label] {
			report.Skipped["alias_collapsed"]++
			continue
		}
		seenLabel[label] = true
		mass := snap.MassOf(t.ID)
		records = append(records, Record{
			Operator: OperatorMass,
			Operands: []string{label},
			Numbers: map[string]any{
				NumberMass:        mass,
				NumberProvisional: t.Provisional,
			},
			State: stateForMass(mass),
		})
	}

	pairs := make([]Pair, 0, len(snap.Pairs))
	for p := range snap.Pairs {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].A != pairs[j].A {
			return pairs[i].A < pairs[j].A
		}
		return pairs[i].B < pairs[j].B
	})
	for _, p := range pairs {
		tagA, okA := snap.Tag(p.A)
		tagB, okB := snap.Tag(p.B)
		if !okA || !okB {
			continue
		}
		labelA := r.reader.Canonical(tagA.Name)
		labelB := r.reader.Canonical(tagB.Name)
		if labelA == labelB {
			report.Skipped["alias_collapsed"]++
			continue
		}
		reading := NewOverlapReading(labelA, labelB, snap.MassOf(p.A), snap.MassOf(p.B), snap.Intersection(p.A, p.B))
		records = append(records, Record{
			Operator: OperatorOverlap,
			Operands: []string{labelA, labelB},
			Numbers: map[string]any{
				NumberIntersection:       reading.Intersection,
				NumberUnion:              reading.Union,
				NumberJaccard:            reading.Jaccard,
				NumberOverlapCoefficient: reading.OverlapCoefficient,
				NumberContainmentA:       reading.ContainmentA,
				NumberContainmentB:       reading.ContainmentB,
				NumberMassA:              reading.MassA,
				NumberMassB:              reading.MassB,
			},
			State: reading.State,
		})
	}

	compounds := r.compounds(snap, report)
	reconstructibility, err := r.reconstructibility(ctx, store, snap, compounds)
	if err != nil {
		return nil, err
	}
	records = append(records, reconstructibility...)
	records = append(records, partCoverageRecords(snap, compounds)...)

	for _, rec := range records {
		report.Operators[string(rec.Operator)]++
		report.States[string(rec.State)]++
	}
	return records, nil
}

type compound struct {
	tag   Tag
	label string
	parts []string
}

func (r *Runner) compounds(snap *Snapshot, report *Report) []compound {
	out := make([]compound, 0, len(snap.Tags))
	for _, t := range snap.Tags {
		switch ClassifyShape(t.Name) {
		case ShapeCompound:
			out = append(out, compound{tag: t, label: r.reader.Canonical(t.Name), parts: DeriveParts(t.Name)})
		case ShapeTooLong:
			report.Skipped["too_long"]++
		case ShapeNonEnglish:
			report.Skipped["non_english"]++
		case ShapeNoParts:
			report.Skipped["no_parts"]++
		}
	}
	return out
}

func (r *Runner) reconstructibility(ctx context.Context, store *Store, snap *Snapshot, compounds []compound) ([]Record, error) {
	var compoundIDs, partIDs []int64
	parts := make([]map[string]Tag, len(compounds))
	for i, c := range compounds {
		parts[i] = make(map[string]Tag, len(c.parts))
		for _, part := range c.parts {
			tag, ok := snap.TagByName(part)
			if !ok {
				continue
			}
			parts[i][part] = tag
			compoundIDs = append(compoundIDs, c.tag.ID)
			partIDs = append(partIDs, tag.ID)
		}
	}

	intersections, err := store.CompoundIntersections(ctx, compoundIDs, partIDs)
	if err != nil {
		return nil, err
	}

	records := make([]Record, 0, len(compounds))
	for i, c := range compounds {
		missing := len(c.parts) - len(parts[i])
		reading := NewReconstructibilityReading(c.label, c.parts, snap.MassOf(c.tag.ID), 0, 0)
		if missing == 0 {
			counts := intersections[c.tag.ID]
			reading = NewReconstructibilityReading(c.label, c.parts, snap.MassOf(c.tag.ID),
				counts.PartsIntersection, counts.Both)
		}
		records = append(records, Record{
			Operator: OperatorReconstructibility,
			Operands: append([]string{c.label}, c.parts...),
			Numbers: map[string]any{
				NumberCompoundMass:      reading.CompoundMass,
				NumberPartsIntersection: reading.PartsIntersection,
				NumberBoth:              reading.Both,
				NumberExcess:            reading.Excess,
				NumberDeficit:           reading.Deficit,
				NumberExcessRatio:       reading.ExcessRatio,
				NumberDeficitRatio:      reading.DeficitRatio,
				NumberPartCount:         len(c.parts),
				NumberMissingParts:      missing,
			},
			State:   reading.State,
			Caveats: []string{CaveatNoAdjectiveFolding},
		})
	}
	return records, nil
}

func partCoverageRecords(snap *Snapshot, compounds []compound) []Record {
	productivity := partProductivity(compounds)
	records := make([]Record, 0, len(compounds))
	for _, c := range compounds {
		for _, part := range c.parts {
			tag, exists := snap.TagByName(part)
			var partMass, inCompound int64
			if exists {
				partMass = snap.MassOf(tag.ID)
				inCompound = snap.Intersection(c.tag.ID, tag.ID)
			}
			reading := NewPartCoverageReading(c.label, part, snap.MassOf(c.tag.ID), partMass, inCompound, productivity[part])
			records = append(records, Record{
				Operator: OperatorPartCoverage,
				Operands: []string{c.label, part},
				Numbers: map[string]any{
					NumberCompoundMass: reading.CompoundMass,
					NumberPartMass:     reading.PartMass,
					NumberInCompound:   reading.InCompound,
					NumberCoverage:     reading.Coverage,
					NumberProductivity: reading.Productivity,
					NumberPartExists:   exists,
				},
				State: reading.State,
			})
		}
	}
	return records
}

func partProductivity(compounds []compound) map[string]int64 {
	out := make(map[string]int64)
	for _, c := range compounds {
		seen := make(map[string]struct{}, len(c.parts))
		for _, part := range c.parts {
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			out[part]++
		}
	}
	return out
}
