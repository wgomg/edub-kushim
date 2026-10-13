package tags

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
)

type storedRecord struct {
	operator       string
	operands       []string
	numbers        map[string]any
	state          string
	ruleSetVersion string
}

func listRunRecords(t *testing.T, client *database.Client, runID uuid.UUID) []storedRecord {
	t.Helper()
	rows, err := client.DB().QueryContext(context.Background(), `
		SELECT operator, to_jsonb(operands), numbers::text, state, rule_set_version
		FROM tag_measurement WHERE run_id = $1`, runID)
	if err != nil {
		t.Fatalf("list measurement records: %v", err)
	}
	defer rows.Close()

	var out []storedRecord
	for rows.Next() {
		var (
			op, numbers, state, version string
			rawOperands                 []byte
		)
		if err := rows.Scan(&op, &rawOperands, &numbers, &state, &version); err != nil {
			t.Fatalf("scan measurement record: %v", err)
		}
		var operands []string
		if err := json.Unmarshal(rawOperands, &operands); err != nil {
			t.Fatalf("unmarshal operands: %v", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(numbers), &parsed); err != nil {
			t.Fatalf("unmarshal numbers: %v", err)
		}
		out = append(out, storedRecord{operator: op, operands: operands, numbers: parsed, state: state, ruleSetVersion: version})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate measurement records: %v", err)
	}
	return out
}

func findRecord(records []storedRecord, operator, firstOperand string) *storedRecord {
	for i := range records {
		if records[i].operator == operator && len(records[i].operands) > 0 && records[i].operands[0] == firstOperand {
			return &records[i]
		}
	}
	return nil
}

func TestRunnerRunWritesRecords(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()

	doc1 := database.CreateUniqueDocument(t, client.Queries, "tagmeasure doc 1")
	doc2 := database.CreateUniqueDocument(t, client.Queries, "tagmeasure doc 2")
	doc3 := database.CreateUniqueDocument(t, client.Queries, "tagmeasure doc 3")
	alpha := database.MustCreateTag(t, client.Queries, "zqalpha")
	beta := database.MustCreateTag(t, client.Queries, "zqbeta")
	compound := database.MustCreateTag(t, client.Queries, "zqalpha zqbeta")
	for _, doc := range []int64{doc1, doc2, doc3} {
		database.MustAddDocumentTag(t, client.Queries, doc, alpha)
		database.MustAddDocumentTag(t, client.Queries, doc, beta)
	}
	database.MustAddDocumentTag(t, client.Queries, doc1, compound)

	runID := uuid.New()
	report, err := NewRunner(client, nil).Run(ctx, runID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if report.RunID != runID.String() {
		t.Errorf("RunID = %q, want %q", report.RunID, runID)
	}
	if report.RuleSetVersion != RuleSetVersion {
		t.Errorf("RuleSetVersion = %q, want %q", report.RuleSetVersion, RuleSetVersion)
	}
	if report.SnapshotAt.IsZero() {
		t.Error("SnapshotAt is zero")
	}
	for _, op := range []string{"mass", "overlap", "reconstructibility", "part_coverage"} {
		if report.Operators[op] == 0 {
			t.Errorf("Operators[%q] = 0, want at least 1", op)
		}
	}
	if report.Worklists.Twins < 1 {
		t.Errorf("Worklists.Twins = %d, want at least 1", report.Worklists.Twins)
	}

	records := listRunRecords(t, client, runID)
	if len(records) != sumCounts(report.Operators) {
		t.Errorf("persisted records = %d, report says %d", len(records), sumCounts(report.Operators))
	}
	for _, r := range records {
		if r.ruleSetVersion != RuleSetVersion {
			t.Errorf("record %v rule_set_version = %q, want %q", r.operands, r.ruleSetVersion, RuleSetVersion)
			break
		}
	}

	mass := findRecord(records, string(OperatorMass), "zqalpha")
	if mass == nil {
		t.Fatal("no mass record for zqalpha")
	}
	if mass.numbers[NumberMass] != float64(3) {
		t.Errorf("mass = %v, want 3", mass.numbers[NumberMass])
	}
	if mass.state != string(StateSufficient) {
		t.Errorf("mass state = %q, want %q", mass.state, StateSufficient)
	}

	unassigned := 0
	for _, r := range records {
		if r.operator == string(OperatorMass) && r.state == string(StateInsufficient) {
			unassigned++
		}
	}
	if unassigned == 0 {
		t.Error("no insufficient mass record for a tag without live documents")
	}

	var pair *storedRecord
	for i := range records {
		if records[i].operator == string(OperatorOverlap) {
			op := append([]string(nil), records[i].operands...)
			if (op[0] == "zqalpha" && op[1] == "zqbeta") || (op[0] == "zqbeta" && op[1] == "zqalpha") {
				pair = &records[i]
				break
			}
		}
	}
	if pair == nil {
		t.Fatal("no overlap record for the zqalpha/zqbeta pair")
	}
	if pair.numbers[NumberIntersection] != float64(3) {
		t.Errorf("intersection = %v, want 3", pair.numbers[NumberIntersection])
	}
	if pair.numbers[NumberJaccard] != float64(1) {
		t.Errorf("jaccard = %v, want 1", pair.numbers[NumberJaccard])
	}

	reconstruct := findRecord(records, string(OperatorReconstructibility), "zqalpha zqbeta")
	if reconstruct == nil {
		t.Fatal("no reconstructibility record for the compound")
	}
	if reconstruct.numbers[NumberCompoundMass] != float64(1) {
		t.Errorf("compound mass = %v, want 1", reconstruct.numbers[NumberCompoundMass])
	}
	if reconstruct.numbers[NumberPartsIntersection] != float64(3) {
		t.Errorf("parts intersection = %v, want 3", reconstruct.numbers[NumberPartsIntersection])
	}
	if reconstruct.numbers[NumberBoth] != float64(1) {
		t.Errorf("both = %v, want 1", reconstruct.numbers[NumberBoth])
	}
	if reconstruct.numbers[NumberDeficit] != float64(2) {
		t.Errorf("deficit = %v, want 2", reconstruct.numbers[NumberDeficit])
	}
	if reconstruct.numbers[NumberMissingParts] != float64(0) {
		t.Errorf("missing parts = %v, want 0", reconstruct.numbers[NumberMissingParts])
	}
	if len(reconstruct.operands) != 3 {
		t.Errorf("operands = %v, want compound plus two parts", reconstruct.operands)
	}
}

func TestRunnerRunFailsOnCanceledContext(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := NewRunner(client, nil).Run(ctx, uuid.New()); err == nil {
		t.Error("Run with a canceled context should fail")
	}
}

func sumCounts(counts map[string]int) int {
	total := 0
	for _, n := range counts {
		total += n
	}
	return total
}

// TestLatestWorklists pins the sweep's read of the measurement substrate: an empty journal is an absent
// marker rather than an error, and only the newest run's records are derived — an older run whose twins
// already clear the gray floor must not leak into the worklists.
func TestLatestWorklists(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	client := database.NewTestClient(t)
	database.ResetTestDatabase(client.DB())
	ctx := context.Background()
	// the reset clears the store tables but leaves the append-only journal, and this test owns its own runs
	if _, err := client.DB().ExecContext(ctx, "DELETE FROM tag_measurement"); err != nil {
		t.Fatalf("clear measurement journal: %v", err)
	}
	store := NewStore(client.Queries)

	absent, err := store.LatestWorklists(ctx)
	if err != nil {
		t.Fatalf("LatestWorklists on an empty journal: %v", err)
	}
	if !absent.Absent {
		t.Error("Absent = false on a store with no measurement run")
	}
	if len(absent.Worklists.Twins) != 0 {
		t.Errorf("Twins = %v, want none", absent.Worklists.Twins)
	}

	older := uuid.New()
	if err := store.InsertRecords(ctx, older, time.Now().Add(-time.Hour), RuleSetVersion, []Record{{
		Operator: OperatorOverlap,
		Operands: []string{"gray a", "gray b"},
		Numbers:  map[string]any{NumberJaccard: 0.8, NumberMassA: int64(4), NumberMassB: int64(4)},
		State:    StateSufficient,
	}}); err != nil {
		t.Fatalf("insert older run: %v", err)
	}
	newest := uuid.New()
	if err := store.InsertRecords(ctx, newest, time.Now(), RuleSetVersion, []Record{{
		Operator: OperatorOverlap,
		Operands: []string{"firm a", "firm b"},
		Numbers:  map[string]any{NumberJaccard: 0.95, NumberMassA: int64(4), NumberMassB: int64(5)},
		State:    StateSufficient,
	}}); err != nil {
		t.Fatalf("insert newest run: %v", err)
	}

	snap, err := store.LatestWorklists(ctx)
	if err != nil {
		t.Fatalf("LatestWorklists: %v", err)
	}
	if snap.Absent {
		t.Fatal("Absent = true after a run exists")
	}
	if snap.RunID != newest {
		t.Errorf("RunID = %s, want the newest run %s", snap.RunID, newest)
	}
	if len(snap.Worklists.Twins) != 1 || snap.Worklists.Twins[0].A != "firm a" {
		t.Errorf("Twins = %+v, want only the newest run's twin", snap.Worklists.Twins)
	}
}
