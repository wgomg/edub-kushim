package tags

// The v1 worklist cuts are provisional: calibration replaces the constants and advances RuleSetVersion.
// The reconstructibility cuts are also inputs to the sweep's composition decision, so recalibrating them
// advances the measurement RuleSetVersion and bumps TagSplitVersion in the same versioned event. The cuts
// stay consumer-side, so a recalibration re-derives an old run's raw numbers instead of invalidating them.
const (
	TwinJaccardCut        = 0.9
	TwinJaccardGrayFloor  = 0.7
	TwinMinMass           = 3
	CompoundSplitCut      = 0.1
	CompoundGrayCut       = 0.25
	SiblingContainmentCut = 0.8
	SiblingGrayFloor      = 0.6
	SiblingMinMass        = 3
)

// ReconstructibilityBand is a compound's reconstructibility under the pinned cuts: firm is the tight
// intersection the parts reconstruct, gray sits in the review band between the firm and gray cuts, far says
// the whole is not its parts' product, and insufficient (a thin or empty extension) supports no band at all.
type ReconstructibilityBand string

// The band values the decision and the worklist cut classify a reading into.
const (
	ReconstructibilityInsufficient ReconstructibilityBand = "insufficient"
	ReconstructibilityFar          ReconstructibilityBand = "far"
	ReconstructibilityGray         ReconstructibilityBand = "gray"
	ReconstructibilityFirm         ReconstructibilityBand = "firm"
)

// ClassifyReconstructibility is the one owner of the band rule: the decision asks it about a live reading
// and the worklist derivation about a stored record, so both agree on where firm ends and review begins.
func ClassifyReconstructibility(state EvidenceState, excessRatio, deficitRatio float64) ReconstructibilityBand {
	if state != StateSufficient {
		return ReconstructibilityInsufficient
	}
	if excessRatio > CompoundGrayCut || deficitRatio > CompoundGrayCut {
		return ReconstructibilityFar
	}
	if excessRatio > CompoundSplitCut || deficitRatio > CompoundSplitCut {
		return ReconstructibilityGray
	}
	return ReconstructibilityFirm
}

// Twin is a pair of tags whose document sets are near-identical: Jaccard
// at or above the gray floor with both masses at or above the minimum.
// Gray marks the review-needed band between the gray floor and
// TwinJaccardCut.
type Twin struct {
	A, B    string
	Jaccard float64
	MassA   int64
	MassB   int64
	Gray    bool
}

// CompoundSplit is a compound tag whose parts reconstruct it, recorded
// only for sufficient readings; ExcessRatio and DeficitRatio say how far
// the parts' coverage deviates from the compound's mass. Gray marks the
// review-needed band above CompoundSplitCut.
type CompoundSplit struct {
	Compound     string
	Parts        []string
	ExcessRatio  float64
	DeficitRatio float64
	Gray         bool
}

// Sibling is a containment pair: A's document set nearly contains B's, at or above the sibling gray floor
// with both masses at or above SiblingMinMass. A is the broader (container) tag and B the narrower one, so
// Containment is the fraction of B's documents that A also holds. Gray marks the review band below
// SiblingContainmentCut.
type Sibling struct {
	A, B        string
	Containment float64
	MassA       int64
	MassB       int64
	Gray        bool
}

// Worklists is the review backlog derived from a measurement sweep: the
// candidate twins, compound splits and siblings, each gray-flagged where a
// human still has to decide.
type Worklists struct {
	Twins          []Twin
	CompoundSplits []CompoundSplit
	Siblings       []Sibling
}

// DeriveWorklists filters evidence readings into the three review
// worklists, applying the v1 cuts per operator. Readings that name too
// few operands, miss their needed numbers, or fall below the floors are
// dropped.
func DeriveWorklists(records []Record) Worklists {
	var w Worklists
	for _, r := range records {
		switch r.Operator {
		case OperatorOverlap:
			w.addOverlap(r)
		case OperatorReconstructibility:
			w.addReconstructibility(r)
		}
	}
	return w
}

func (w *Worklists) addOverlap(r Record) {
	if len(r.Operands) < 2 {
		return
	}
	jaccard, ok := numberFloat64(r.Numbers, NumberJaccard)
	if !ok {
		return
	}
	massA, _ := numberInt64(r.Numbers, NumberMassA)
	massB, _ := numberInt64(r.Numbers, NumberMassB)

	if jaccard >= TwinJaccardGrayFloor && massA >= TwinMinMass && massB >= TwinMinMass {
		w.Twins = append(w.Twins, Twin{
			A:       r.Operands[0],
			B:       r.Operands[1],
			Jaccard: jaccard,
			MassA:   massA,
			MassB:   massB,
			Gray:    jaccard < TwinJaccardCut,
		})
	}

	containmentA, _ := numberFloat64(r.Numbers, NumberContainmentA)
	containmentB, _ := numberFloat64(r.Numbers, NumberContainmentB)
	containment := max(containmentA, containmentB)
	if containment >= SiblingGrayFloor && massA >= SiblingMinMass && massB >= SiblingMinMass {
		// containment_a is the fraction of a's documents that also carry b, so the larger ratio belongs to
		// the contained tag; order the pair container first and carry each mass with its own name
		container, contained := r.Operands[0], r.Operands[1]
		containerMass, containedMass := massA, massB
		if containmentA > containmentB {
			container, contained = r.Operands[1], r.Operands[0]
			containerMass, containedMass = massB, massA
		}
		w.Siblings = append(w.Siblings, Sibling{
			A:           container,
			B:           contained,
			Containment: containment,
			MassA:       containerMass,
			MassB:       containedMass,
			Gray:        containment < SiblingContainmentCut,
		})
	}
}

func (w *Worklists) addReconstructibility(r Record) {
	if r.State != StateSufficient {
		return
	}
	excess, okExcess := numberFloat64(r.Numbers, NumberExcessRatio)
	deficit, okDeficit := numberFloat64(r.Numbers, NumberDeficitRatio)
	if !okExcess || !okDeficit {
		return
	}
	band := ClassifyReconstructibility(r.State, excess, deficit)
	if band != ReconstructibilityFirm && band != ReconstructibilityGray {
		return
	}
	var parts []string
	if len(r.Operands) > 1 {
		parts = r.Operands[1:]
	}
	w.CompoundSplits = append(w.CompoundSplits, CompoundSplit{
		Compound:     r.Operands[0],
		Parts:        parts,
		ExcessRatio:  excess,
		DeficitRatio: deficit,
		Gray:         band == ReconstructibilityGray,
	})
}
