package types

import "slices"

import "testing"

func TestAllCounts(t *testing.T) {
	if got, want := len(allBatchOrigins), 6; got != want {
		t.Errorf("batch origins len = %d, want %d", got, want)
	}
	if got, want := len(AllBatchStatuses()), 6; got != want {
		t.Errorf("AllBatchStatuses len = %d, want %d", got, want)
	}
	if got, want := len(AllTaskTypes()), 12; got != want {
		t.Errorf("AllTaskTypes len = %d, want %d", got, want)
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
	for _, st := range allTaskStatuses {
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

func TestHoldsLocks(t *testing.T) {
	cases := map[TaskType]bool{
		Task.Type.Backup:         true,
		Task.Type.Mirror:         true,
		Task.Type.TagReconcile:   true,
		Task.Type.ThumbBackfill:  true,
		Task.Type.TagMeasure:     true,
		Task.Type.TagIdentity:    true,
		Task.Type.TagAudit:       true,
		Task.Type.MigrateDB:      true,
		Task.Type.MigrateStorage: true,
		Task.Type.Consume:        false,
		Task.Type.Enrich:         false,
		Task.Type.Thumbnail:      false,
	}
	for typ, want := range cases {
		if got := typ.HoldsLocks(); got != want {
			t.Errorf("TaskType(%q).HoldsLocks() = %v, want %v", typ, got, want)
		}
	}
}

func TestLockDeclarations(t *testing.T) {
	contains := func(list []TaskType, want TaskType) bool {
		return slices.Contains(list, want)
	}

	for _, want := range []TaskType{Task.Type.Consume, Task.Type.Enrich, Task.Type.Thumbnail, Task.Type.ThumbBackfill} {
		if !contains(TaskTypesWaitingOn(LockMaintenance), want) {
			t.Errorf("TaskTypesWaitingOn(maintenance) misses %q", want)
		}
	}
	for _, want := range []TaskType{Task.Type.Thumbnail, Task.Type.Backup, Task.Type.Mirror, Task.Type.MigrateStorage} {
		if !contains(TaskTypesWaitingOn(LockStorage), want) {
			t.Errorf("TaskTypesWaitingOn(storage) misses %q", want)
		}
	}

	holding := LockHoldingTaskTypes()
	if contains(holding, Task.Type.Consume) {
		t.Error("consume holds no lock")
	}
	if !contains(holding, Task.Type.ThumbBackfill) || !contains(holding, Task.Type.MigrateStorage) {
		t.Error("LockHoldingTaskTypes misses a declared holder")
	}

	if declaration := LockDeclarationFor(TaskType("no-such-type")); len(declaration.Holds)+len(declaration.WaitsFor) != 0 {
		t.Errorf("an undeclared type reports locks: %+v", declaration)
	}
	if got := len(AllLockNames()); got != 2 {
		t.Errorf("AllLockNames len = %d, want 2", got)
	}
}
