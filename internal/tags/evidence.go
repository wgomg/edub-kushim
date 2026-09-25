package tags

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// RuleSetVersion pins the algebra's rules and the worklist cuts; a bump
// makes earlier readings stale, not wrong.
const RuleSetVersion = "v0"

// The Number* keys name the values a Record's Numbers map carries for each
// operator; the readings fill them and the worklist cuts read them.
const (
	NumberMass               = "mass"
	NumberProvisional        = "provisional"
	NumberIntersection       = "intersection"
	NumberUnion              = "union"
	NumberJaccard            = "jaccard"
	NumberOverlapCoefficient = "overlap_coefficient"
	NumberContainmentA       = "containment_a"
	NumberContainmentB       = "containment_b"
	NumberMassA              = "mass_a"
	NumberMassB              = "mass_b"
	NumberCompoundMass       = "compound_mass"
	NumberPartsIntersection  = "parts_intersection"
	NumberBoth               = "both"
	NumberExcess             = "excess"
	NumberDeficit            = "deficit"
	NumberExcessRatio        = "excess_ratio"
	NumberDeficitRatio       = "deficit_ratio"
	NumberPartCount          = "part_count"
	NumberMissingParts       = "missing_parts"
	NumberPartMass           = "part_mass"
	NumberInCompound         = "in_compound"
	NumberCoverage           = "coverage"
	NumberProductivity       = "productivity"
	NumberPartExists         = "part_exists"
)

// CaveatNoAdjectiveFolding marks reconstructibility readings whose part
// matching does not fold adjective forms, so the reading is an
// approximation of true part coverage.
const CaveatNoAdjectiveFolding = "no_adjective_folding"

// Record is one evidence reading: the operator applied to its operands,
// the numbers the algebra computed, the state the reading ended in, and
// caveats that qualify it. RunID and SnapshotAt tie the reading to a
// measurement run.
type Record struct {
	RunID          uuid.UUID
	SnapshotAt     time.Time
	RuleSetVersion string
	Operator       Operator
	Operands       []string
	Numbers        map[string]any
	State          EvidenceState
	Caveats        []string
}

type measurementRow struct {
	Operator string          `json:"operator"`
	Operands []string        `json:"operands"`
	Numbers  json.RawMessage `json:"numbers"`
	State    string          `json:"state"`
	Caveats  []string        `json:"caveats"`
}

// EncodeRows serializes readings into the JSON stored on the measurement
// rows. A nil caveat list encodes as an empty array, never null.
func EncodeRows(records []Record) (json.RawMessage, error) {
	rows := make([]measurementRow, 0, len(records))
	for _, r := range records {
		numbers, err := json.Marshal(r.Numbers)
		if err != nil {
			return nil, err
		}
		caveats := r.Caveats
		if caveats == nil {
			caveats = []string{}
		}
		rows = append(rows, measurementRow{
			Operator: string(r.Operator),
			Operands: r.Operands,
			Numbers:  numbers,
			State:    string(r.State),
			Caveats:  caveats,
		})
	}
	return json.Marshal(rows)
}

func numberInt64(numbers map[string]any, key string) (int64, bool) {
	switch v := numbers[key].(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	}
	return 0, false
}

func numberFloat64(numbers map[string]any, key string) (float64, bool) {
	switch v := numbers[key].(type) {
	case float64:
		return v, true
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}
