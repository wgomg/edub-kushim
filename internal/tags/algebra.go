package tags

type Set map[int64]struct{}

func NewSet(ids ...int64) Set {
	s := make(Set, len(ids))
	for _, id := range ids {
		s[id] = struct{}{}
	}
	return s
}

func (s Set) Size() int64 { return int64(len(s)) }

func (s Set) Contains(id int64) bool {
	_, ok := s[id]
	return ok
}

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

func (s Set) Difference(other Set) Set {
	out := make(Set, len(s))
	for id := range s {
		if _, ok := other[id]; !ok {
			out[id] = struct{}{}
		}
	}
	return out
}

type EvidenceState string

const (
	StateSufficient   EvidenceState = "sufficient"
	StateInsufficient EvidenceState = "insufficient"
	StateGray         EvidenceState = "gray"
)

type Operator string

const (
	OperatorMass               Operator = "mass"
	OperatorOverlap            Operator = "overlap"
	OperatorEquality           Operator = "equality"
	OperatorContainment        Operator = "containment"
	OperatorReconstructibility Operator = "reconstructibility"
	OperatorPartCoverage       Operator = "part_coverage"
	OperatorComplement         Operator = "complement"
)

type MassReading struct {
	Tag   string
	Mass  int64
	State EvidenceState
}

func MassOf(tag string, s Set) MassReading {
	return MassReading{Tag: tag, Mass: s.Size(), State: stateForMass(s.Size())}
}

func stateForMass(mass int64) EvidenceState {
	if mass == 0 {
		return StateInsufficient
	}
	return StateSufficient
}

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

func OverlapOf(a, b string, sa, sb Set) OverlapReading {
	return NewOverlapReading(a, b, sa.Size(), sb.Size(), sa.Intersect(sb).Size())
}

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

type EqualityReading struct {
	A, B          string
	SetSimilarity float64
	State         EvidenceState
}

func EqualityOf(a, b string, sa, sb Set) EqualityReading {
	return NewEqualityReading(a, b, sa.Size(), sb.Size(), sa.Intersect(sb).Size())
}

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

type ContainmentReading struct {
	Container    string
	Contained    string
	Intersection int64
	Mass         int64
	Ratio        float64
	State        EvidenceState
}

func ContainmentOf(container, contained string, containerSet, containedSet Set) ContainmentReading {
	return NewContainmentReading(container, contained, containerSet.Size(), containedSet.Size(),
		containerSet.Intersect(containedSet).Size())
}

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

func PartCoverageOf(compound, part string, compoundSet, partSet Set, productivity int64) PartCoverageReading {
	return NewPartCoverageReading(compound, part, compoundSet.Size(), partSet.Size(),
		compoundSet.Intersect(partSet).Size(), productivity)
}

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

type ComplementReading struct {
	A, B  string
	Size  int64
	State EvidenceState
}

func ComplementOf(a, b string, sa, sb Set) ComplementReading {
	return NewComplementReading(a, b, sa.Size(), sa.Intersect(sb).Size())
}

func NewComplementReading(a, b string, massA, intersection int64) ComplementReading {
	r := ComplementReading{A: a, B: b, Size: massA - intersection, State: StateSufficient}
	if massA == 0 {
		r.State = StateInsufficient
	}
	return r
}
