package types

import "testing"

func TestAllCounts(t *testing.T) {
	if got, want := len(allBatchOrigins), 6; got != want {
		t.Errorf("batch origins len = %d, want %d", got, want)
	}
	if got, want := len(AllBatchStatuses()), 6; got != want {
		t.Errorf("AllBatchStatuses len = %d, want %d", got, want)
	}
	if got, want := len(AllTaskTypes()), 13; got != want {
		t.Errorf("AllTaskTypes len = %d, want %d", got, want)
	}
	if got, want := len(AllTaskStatuses()), 7; got != want {
		t.Errorf("AllTaskStatuses len = %d, want %d", got, want)
	}
	if got, want := len(NonConsumeTaskTypes()), 9; got != want {
		t.Errorf("NonConsumeTaskTypes len = %d, want %d", got, want)
	}
}

func TestValid(t *testing.T) {
	originCases := map[string]bool{
		string(Batch.Origin.CLI):      true,
		string(Batch.Origin.Schedule): true,
		string(Batch.Origin.Wizard):   true,
		"upload":                      false,
		"unknown":                     false,
	}
	for s, want := range originCases {
		if got := BatchOrigin(s).Valid(); got != want {
			t.Errorf("BatchOrigin(%q).Valid() = %v, want %v", s, got, want)
		}
	}
	taskCases := map[string]bool{
		string(Task.Status.Pending):   true,
		string(Task.Status.Discarded): true,
		"active":                      false,
		"unknown":                     false,
	}
	for s, want := range taskCases {
		if got := TaskStatus(s).Valid(); got != want {
			t.Errorf("TaskStatus(%q).Valid() = %v, want %v", s, got, want)
		}
	}
}

func TestParseRoundTrip(t *testing.T) {
	for _, st := range AllTaskStatuses() {
		got, err := ParseTaskStatus(string(st))
		if err != nil {
			t.Errorf("ParseTaskStatus(%q) error: %v", st, err)
		}
		if got != st {
			t.Errorf("ParseTaskStatus round-trip mismatch: got %q, want %q", got, st)
		}
	}
	if _, err := ParseTaskStatus("active"); err == nil {
		t.Error("ParseTaskStatus(\"active\") expected error")
	}
}

func TestAllReturnsClone(t *testing.T) {
	a := AllTaskTypes()
	b := AllTaskTypes()
	if &a[0] == &b[0] {
		t.Error("AllTaskTypes returned the same backing slice twice")
	}
	a[0] = TaskType("mutated")
	if AllTaskTypes()[0] == TaskType("mutated") {
		t.Error("AllTaskTypes did not return an independent clone")
	}
}

func TestNonConsumeTaskTypes(t *testing.T) {
	want := map[TaskType]bool{
		Task.Type.Config:         true,
		Task.Type.MigrateDB:      true,
		Task.Type.MigrateStorage: true,
		Task.Type.Backup:         true,
		Task.Type.Mirror:         true,
		Task.Type.TagReconcile:   true,
		Task.Type.TagMeasure:     true,
		Task.Type.TagIdentity:    true,
		Task.Type.TagAudit:       true,
	}
	values := NonConsumeTaskTypeValues()
	if len(values) != len(want) {
		t.Fatalf("NonConsumeTaskTypeValues len = %d, want %d", len(values), len(want))
	}
	for i, typ := range NonConsumeTaskTypes() {
		if !want[typ] {
			t.Errorf("unexpected non-consume task type: %q", typ)
		}
		if values[i] != string(typ) {
			t.Errorf("value %q does not match type %q", values[i], typ)
		}
	}
}

func TestIsBlocking(t *testing.T) {
	cases := map[TaskType]bool{
		Task.Type.Backup:        true,
		Task.Type.Mirror:        true,
		Task.Type.TagReconcile:  true,
		Task.Type.ThumbBackfill: true,
		Task.Type.TagMeasure:    true,
		Task.Type.TagIdentity:   true,
		Task.Type.TagAudit:      true,
		Task.Type.Config:        false,
	}
	for typ, want := range cases {
		if got := typ.IsBlocking(); got != want {
			t.Errorf("TaskType(%q).IsBlocking() = %v, want %v", typ, got, want)
		}
	}
}

func TestIsLockGated(t *testing.T) {
	cases := map[TaskType]bool{
		Task.Type.Consume:       true,
		Task.Type.Enrich:        true,
		Task.Type.Thumbnail:     true,
		Task.Type.TagReconcile:  false,
		Task.Type.ThumbBackfill: false,
		Task.Type.TagMeasure:    false,
		Task.Type.TagIdentity:   false,
		Task.Type.Config:        false,
	}
	for typ, want := range cases {
		if got := typ.IsLockGated(); got != want {
			t.Errorf("TaskType(%q).IsLockGated() = %v, want %v", typ, got, want)
		}
	}
}
