package tags

import (
	"encoding/json"
	"testing"
)

func TestEncodeRows(t *testing.T) {
	records := []Record{
		{
			Operator: OperatorMass,
			Operands: []string{"history"},
			Numbers:  map[string]any{NumberMass: int64(4)},
			State:    StateSufficient,
		},
		{
			Operator: OperatorReconstructibility,
			Operands: []string{"bike repair", "bike", "repair"},
			Numbers:  map[string]any{NumberExcessRatio: 0.5},
			State:    StateInsufficient,
			Caveats:  []string{CaveatNoAdjectiveFolding},
		},
	}

	raw, err := EncodeRows(records)
	if err != nil {
		t.Fatalf("EncodeRows: %v", err)
	}

	var rows []struct {
		Operator string          `json:"operator"`
		Operands []string        `json:"operands"`
		Numbers  json.RawMessage `json:"numbers"`
		State    string          `json:"state"`
		Caveats  []string        `json:"caveats"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("unmarshal rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("row count = %d, want 2", len(rows))
	}
	if rows[0].Caveats == nil {
		t.Error("nil caveats encoded as JSON null; the store column is NOT NULL")
	}
	if rows[0].Operator != string(OperatorMass) || rows[0].State != string(StateSufficient) {
		t.Errorf("row 0 operator/state = %q/%q", rows[0].Operator, rows[0].State)
	}
	if len(rows[1].Operands) != 3 || rows[1].Operands[1] != "bike" {
		t.Errorf("row 1 operands = %v", rows[1].Operands)
	}
	if len(rows[1].Caveats) != 1 || rows[1].Caveats[0] != CaveatNoAdjectiveFolding {
		t.Errorf("row 1 caveats = %v", rows[1].Caveats)
	}

	var numbers map[string]any
	if err := json.Unmarshal(rows[0].Numbers, &numbers); err != nil {
		t.Fatalf("unmarshal numbers: %v", err)
	}
	if mass, ok := numbers[NumberMass].(float64); !ok || mass != 4 {
		t.Errorf("numbers[%q] = %v, want 4", NumberMass, numbers[NumberMass])
	}
}
