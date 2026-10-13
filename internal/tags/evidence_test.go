package tags

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
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

func TestEncodeRows_NormalizesOperands(t *testing.T) {
	raw, err := EncodeRows([]Record{{
		Operator: OperatorReconstructibility,
		Operands: []string{" World War II ", "Bike Repair"},
		Numbers:  map[string]any{NumberExcessRatio: 0.5},
		State:    StateInsufficient,
	}})
	if err != nil {
		t.Fatalf("EncodeRows: %v", err)
	}

	var rows []struct {
		Operands []string `json:"operands"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("unmarshal rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(rows))
	}
	want := []string{"world war ii", "bike repair"}
	if len(rows[0].Operands) != len(want) {
		t.Fatalf("operands = %v, want %v", rows[0].Operands, want)
	}
	for i := range want {
		if rows[0].Operands[i] != want[i] {
			t.Errorf("operands[%d] = %q, want %q (normalized at the write boundary)", i, rows[0].Operands[i], want[i])
		}
	}
}

// TestDecodeRows pins the read side of the measurement boundary: what EncodeRows persisted comes back typed
// for the worklist cuts, and a malformed numbers payload fails with the offending row instead of decoding as
// a silent zero that would fall below every cut.
func TestDecodeRows(t *testing.T) {
	records := []Record{
		{
			Operator: OperatorMass,
			Operands: []string{"history"},
			Numbers:  map[string]any{NumberMass: int64(4)},
			State:    StateSufficient,
		},
		{
			Operator: OperatorReconstructibility,
			Operands: []string{"bike repair"},
			Numbers:  map[string]any{NumberExcessRatio: 0.5},
			State:    StateInsufficient,
			Caveats:  []string{CaveatNoAdjectiveFolding},
		},
	}

	raw, err := EncodeRows(records)
	if err != nil {
		t.Fatalf("EncodeRows: %v", err)
	}
	var rows []database.TagMeasurementRecordsForRunRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("unmarshal persisted rows: %v", err)
	}
	got, err := DecodeRows(rows)
	if err != nil {
		t.Fatalf("DecodeRows: %v", err)
	}
	if len(got) != len(records) {
		t.Fatalf("decoded = %d records, want %d", len(got), len(records))
	}
	for i, want := range records {
		if got[i].Operator != want.Operator {
			t.Errorf("row %d operator = %q, want %q", i, got[i].Operator, want.Operator)
		}
		if got[i].State != want.State {
			t.Errorf("row %d state = %q, want %q", i, got[i].State, want.State)
		}
		if len(got[i].Operands) != len(want.Operands) || got[i].Operands[0] != want.Operands[0] {
			t.Errorf("row %d operands = %v, want %v", i, got[i].Operands, want.Operands)
		}
	}
	if len(got[1].Caveats) != 1 || got[1].Caveats[0] != CaveatNoAdjectiveFolding {
		t.Errorf("caveats = %v, want the persisted caveat back", got[1].Caveats)
	}
	// the cuts read both an integer mass and a float ratio through numberFloat64, which is what the JSON
	// round-trip hands back
	if mass, ok := numberFloat64(got[0].Numbers, NumberMass); !ok || mass != 4 {
		t.Errorf("numbers[%q] = %v, want 4 readable by the cuts", NumberMass, got[0].Numbers[NumberMass])
	}
	if ratio, ok := numberFloat64(got[1].Numbers, NumberExcessRatio); !ok || ratio != 0.5 {
		t.Errorf("numbers[%q] = %v, want 0.5 readable by the cuts", NumberExcessRatio, got[1].Numbers[NumberExcessRatio])
	}

	// the run-scoped columns belong to the run, not to the row: decoding never stamps them
	if got[0].RunID != uuid.Nil || !got[0].SnapshotAt.IsZero() || got[0].RuleSetVersion != "" {
		t.Errorf("run columns = (%v, %v, %q), want zero: the derivation never reads them",
			got[0].RunID, got[0].SnapshotAt, got[0].RuleSetVersion)
	}

	_, err = DecodeRows([]database.TagMeasurementRecordsForRunRow{{
		Operator: string(OperatorMass),
		Numbers:  json.RawMessage("{"),
	}})
	if err == nil {
		t.Fatal("a malformed numbers payload must fail, not decode to zero")
	}
	if !strings.Contains(err.Error(), "row 0") {
		t.Errorf("error = %q, want the offending row's index", err)
	}
}
