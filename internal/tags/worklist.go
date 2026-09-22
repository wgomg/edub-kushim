package tags

// v0 cuts are provisional: calibration replaces the constants and advances
// RuleSetVersion; cuts stay consumer-side so recalibration never invalidates
// raw records
const (
	TwinJaccardCut        = 0.9
	TwinJaccardGrayFloor  = 0.7
	TwinMinMass           = 2
	CompoundSplitCut      = 0.1
	CompoundGrayCut       = 0.25
	SiblingContainmentCut = 0.8
	SiblingGrayFloor      = 0.6
)

type Twin struct {
	A, B    string
	Jaccard float64
	MassA   int64
	MassB   int64
	Gray    bool
}

type CompoundSplit struct {
	Compound     string
	Parts        []string
	ExcessRatio  float64
	DeficitRatio float64
	Gray         bool
}

type Sibling struct {
	A, B        string
	Containment float64
	Gray        bool
}

type Worklists struct {
	Twins          []Twin
	CompoundSplits []CompoundSplit
	Siblings       []Sibling
}

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
	if containment >= SiblingGrayFloor {
		w.Siblings = append(w.Siblings, Sibling{
			A:           r.Operands[0],
			B:           r.Operands[1],
			Containment: containment,
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
	if excess > CompoundGrayCut || deficit > CompoundGrayCut {
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
		Gray:         excess > CompoundSplitCut || deficit > CompoundSplitCut,
	})
}
