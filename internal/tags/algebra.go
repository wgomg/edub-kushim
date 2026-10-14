// Package tags is the tag subsystem's domain core: the evidence algebra
// (document-ID sets, readings and their sufficiency states), the atomicity
// shape classification, the staged admission decision, the derived review
// worklists, the measurement sweep, and the snapshot view the sweep reads.
// The subsystem's contracts are specified in docs/tags (entry point:
// docs/tags/overview.md); the pinned admission rules live in tags/policy,
// the LLM machinery in tags/admission, the human operations in
// tags/curation, and the scheduled maintenance in tags/reconcile.
package tags

// Set is a set of document IDs, the unit the evidence algebra measures: a
// tag's document set is its mass.
type Set map[int64]struct{}

// NewSet builds a set from the given ids.
func NewSet(ids ...int64) Set {
	s := make(Set, len(ids))
	for _, id := range ids {
		s[id] = struct{}{}
	}
	return s
}

// Size returns the set's cardinality, the mass of a tag.
func (s Set) Size() int64 { return int64(len(s)) }

// Contains reports whether the id is in the set.
func (s Set) Contains(id int64) bool {
	_, ok := s[id]
	return ok
}

// Union returns a new set with the ids of both sets.
func (s Set) Union(other Set) Set {
	out := make(Set, len(s)+len(other))
	for id := range s {
		out[id] = struct{}{}
	}
	for id := range other {
		out[id] = struct{}{}
	}
	return out
}

// Intersect returns a new set with the ids present in both sets.
func (s Set) Intersect(other Set) Set {
	small, large := s, other
	if len(large) < len(small) {
		small, large = large, small
	}
	out := make(Set, len(small))
	for id := range small {
		if _, ok := large[id]; ok {
			out[id] = struct{}{}
		}
	}
	return out
}

// Difference returns a new set with the ids of s not present in other.
func (s Set) Difference(other Set) Set {
	out := make(Set, len(s))
	for id := range s {
		if _, ok := other[id]; !ok {
			out[id] = struct{}{}
		}
	}
	return out
}

// EvidenceState is the sufficiency verdict of a reading: whether its
// numbers support the consumer's cuts. The readings produce sufficient and
// insufficient; gray belongs to the vocabulary but is not produced by
// today's readings.
type EvidenceState string

// The three states a reading can carry.
const (
	StateSufficient   EvidenceState = "sufficient"
	StateInsufficient EvidenceState = "insufficient"
	StateGray         EvidenceState = "gray"
)

// Operator names one evidence operator of the algebra.
type Operator string

// The operators the algebra computes; each has a matching reading type and
// Record numbers, and the worklist derivation keys on the overlap and
// reconstructibility readings.
const (
	OperatorMass               Operator = "mass"
	OperatorOverlap            Operator = "overlap"
	OperatorEquality           Operator = "equality"
	OperatorContainment        Operator = "containment"
	OperatorReconstructibility Operator = "reconstructibility"
	OperatorPartCoverage       Operator = "part_coverage"
	OperatorComplement         Operator = "complement"
	OperatorCoverage           Operator = "coverage"
)

// MassReading is a tag's document-set mass.
type MassReading struct {
	Tag   string
	Mass  int64
	State EvidenceState
}

// MassOf reads a set's size as a tag's mass.
func MassOf(tag string, s Set) MassReading {
	return MassReading{Tag: tag, Mass: s.Size(), State: stateForMass(s.Size())}
}

func stateForMass(mass int64) EvidenceState {
	if mass == 0 {
		return StateInsufficient
	}
	return StateSufficient
}

// OverlapReading compares two tags' document sets: intersection, union,
// Jaccard and overlap coefficients, and per-side containment.
type OverlapReading struct {
	A, B               string
	Intersection       int64
	Union              int64
	Jaccard            float64
	OverlapCoefficient float64
	ContainmentA       float64
	ContainmentB       float64
	MassA, MassB       int64
	State              EvidenceState
}

// OverlapOf computes the reading from two sets; NewOverlapReading is its
// count-based form, the shape the measurement sweep stores.
func OverlapOf(a, b string, sa, sb Set) OverlapReading {
	return NewOverlapReading(a, b, sa.Size(), sb.Size(), sa.Intersect(sb).Size())
}

// NewOverlapReading computes the reading from the two masses and their
// intersection. A zero mass on either side makes the reading insufficient;
// the ratios are zero whenever their denominator would be.
func NewOverlapReading(a, b string, massA, massB, intersection int64) OverlapReading {
	union := massA + massB - intersection
	r := OverlapReading{
		A:            a,
		B:            b,
		Intersection: intersection,
		Union:        union,
		MassA:        massA,
		MassB:        massB,
		State:        StateSufficient,
	}
	if union > 0 {
		r.Jaccard = float64(intersection) / float64(union)
	}
	if minMass := min(massA, massB); minMass > 0 {
		r.OverlapCoefficient = float64(intersection) / float64(minMass)
	}
	if massA > 0 {
		r.ContainmentA = float64(intersection) / float64(massA)
	}
	if massB > 0 {
		r.ContainmentB = float64(intersection) / float64(massB)
	}
	if massA == 0 || massB == 0 {
		r.State = StateInsufficient
	}
	return r
}

// EqualityReading is the set similarity (Jaccard) of two tags' document
// sets.
type EqualityReading struct {
	A, B          string
	SetSimilarity float64
	State         EvidenceState
}

// EqualityOf computes the reading from two sets.
func EqualityOf(a, b string, sa, sb Set) EqualityReading {
	return NewEqualityReading(a, b, sa.Size(), sb.Size(), sa.Intersect(sb).Size())
}

// NewEqualityReading computes the reading from the two masses and their
// intersection; a zero mass on either side makes the reading insufficient.
func NewEqualityReading(a, b string, massA, massB, intersection int64) EqualityReading {
	r := EqualityReading{A: a, B: b, State: StateSufficient}
	if union := massA + massB - intersection; union > 0 {
		r.SetSimilarity = float64(intersection) / float64(union)
	}
	if massA == 0 || massB == 0 {
		r.State = StateInsufficient
	}
	return r
}

// ContainmentReading says how much of the contained tag's document set
// the container's set covers; Mass is the contained set's size.
type ContainmentReading struct {
	Container    string
	Contained    string
	Intersection int64
	Mass         int64
	Ratio        float64
	State        EvidenceState
}

// ContainmentOf computes the reading from two sets; the container is the
// side whose coverage is measured.
func ContainmentOf(container, contained string, containerSet, containedSet Set) ContainmentReading {
	return NewContainmentReading(container, contained, containerSet.Size(), containedSet.Size(),
		containerSet.Intersect(containedSet).Size())
}

// NewContainmentReading computes the reading from the two masses and
// their intersection. A zero mass on either side makes the reading
// insufficient.
func NewContainmentReading(container, contained string, containerMass, containedMass, intersection int64) ContainmentReading {
	r := ContainmentReading{
		Container:    container,
		Contained:    contained,
		Intersection: intersection,
		Mass:         containedMass,
		State:        StateSufficient,
	}
	if containedMass > 0 {
		r.Ratio = float64(intersection) / float64(containedMass)
	}
	if containerMass == 0 || containedMass == 0 {
		r.State = StateInsufficient
	}
	return r
}

// ReconstructibilityReading says how well a compound tag's parts
// reconstruct its document set: Excess is the compound mass the parts do
// not carry, Deficit is the parts' intersection beyond the compound.
// ExcessRatio and DeficitRatio relate each to its own mass.
type ReconstructibilityReading struct {
	Compound          string
	Parts             []string
	CompoundMass      int64
	PartsIntersection int64
	Both              int64
	Excess            int64
	Deficit           int64
	ExcessRatio       float64
	DeficitRatio      float64
	State             EvidenceState
}

// ReconstructibilityOf computes the reading from sets; partSets are the
// parts' document sets in part order.
func ReconstructibilityOf(compound string, parts []string, compoundSet Set, partSets []Set) ReconstructibilityReading {
	intersection := Set{}
	if len(partSets) > 0 {
		intersection = partSets[0].Union(nil)
		for _, p := range partSets[1:] {
			intersection = intersection.Intersect(p)
		}
	}
	return NewReconstructibilityReading(compound, parts, compoundSet.Size(), intersection.Size(),
		compoundSet.Intersect(intersection).Size())
}

// NewReconstructibilityReading computes the reading from the compound's
// mass, the intersection of the parts' sets, and their overlap with the
// compound. A zero compound or parts-intersection mass makes the reading
// insufficient.
func NewReconstructibilityReading(compound string, parts []string, compoundMass, partsIntersection, both int64) ReconstructibilityReading {
	r := ReconstructibilityReading{
		Compound:          compound,
		Parts:             parts,
		CompoundMass:      compoundMass,
		PartsIntersection: partsIntersection,
		Both:              both,
		Excess:            compoundMass - both,
		Deficit:           partsIntersection - both,
		State:             StateSufficient,
	}
	if compoundMass > 0 {
		r.ExcessRatio = float64(r.Excess) / float64(compoundMass)
	}
	if partsIntersection > 0 {
		r.DeficitRatio = float64(r.Deficit) / float64(partsIntersection)
	}
	if compoundMass == 0 || partsIntersection == 0 {
		r.State = StateInsufficient
	}
	return r
}

// PartCoverageReading is one part's contribution to its compound:
// Coverage is the part's share of the compound's documents and
// Productivity counts how many compounds the part serves.
type PartCoverageReading struct {
	Compound     string
	Part         string
	CompoundMass int64
	PartMass     int64
	InCompound   int64
	Coverage     float64
	Productivity int64
	State        EvidenceState
}

// PartCoverageOf computes the reading from the compound's and the part's
// sets.
func PartCoverageOf(compound, part string, compoundSet, partSet Set, productivity int64) PartCoverageReading {
	return NewPartCoverageReading(compound, part, compoundSet.Size(), partSet.Size(),
		compoundSet.Intersect(partSet).Size(), productivity)
}

// NewPartCoverageReading computes the reading from the raw counts. A zero
// compound or part mass makes the reading insufficient.
func NewPartCoverageReading(compound, part string, compoundMass, partMass, inCompound, productivity int64) PartCoverageReading {
	r := PartCoverageReading{
		Compound:     compound,
		Part:         part,
		CompoundMass: compoundMass,
		PartMass:     partMass,
		InCompound:   inCompound,
		Productivity: productivity,
		State:        StateSufficient,
	}
	if compoundMass > 0 {
		r.Coverage = float64(inCompound) / float64(compoundMass)
	}
	if compoundMass == 0 || partMass == 0 {
		r.State = StateInsufficient
	}
	return r
}

// ComplementReading is the mass of documents tagged A but not B, as Size.
type ComplementReading struct {
	A, B  string
	Size  int64
	State EvidenceState
}

// ComplementOf computes the reading from the two sets.
func ComplementOf(a, b string, sa, sb Set) ComplementReading {
	return NewComplementReading(a, b, sa.Size(), sa.Intersect(sb).Size())
}

// NewComplementReading computes the reading from A's mass and the
// intersection; a zero mass makes the reading insufficient.
func NewComplementReading(a, b string, massA, intersection int64) ComplementReading {
	r := ComplementReading{A: a, B: b, Size: massA - intersection, State: StateSufficient}
	if massA == 0 {
		r.State = StateInsufficient
	}
	return r
}
