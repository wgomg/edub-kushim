package types

import "slices"

import "testing"

func TestAllCounts(t *testing.T) {
	if got, want := len(allBatchOrigins), 7; got != want {
		t.Errorf("batch origins len = %d, want %d", got, want)
	}
	if got, want := len(AllBatchStatuses()), 6; got != want {
		t.Errorf("AllBatchStatuses len = %d, want %d", got, want)
	}
	if got, want := len(AllTaskTypes()), 14; got != want {
		t.Errorf("AllTaskTypes len = %d, want %d", got, want)
	}
}

func TestValidTagRelation(t *testing.T) {
	cases := map[string]bool{
		string(TagRelations.SameConcept): true,
		string(TagRelations.NearSynonym): true,
		string(TagRelations.Distinct):    true,
		string(TagRelations.Uncertain):   true,
		"same":                           false,
		"variant":                        false,
		"tie":                            false,
		"":                               false,
	}
	for s, want := range cases {
		if got := TagRelation(s).Valid(); got != want {
			t.Errorf("TagRelation(%q).Valid() = %v, want %v", s, got, want)
		}
		if want {
			if got, err := ParseTagRelation(s); err != nil || got != TagRelation(s) {
				t.Errorf("ParseTagRelation(%q) = %q, %v; want %q, nil", s, got, err, s)
			}
		}
	}
	if _, err := ParseTagRelation("same"); err == nil {
		t.Error("ParseTagRelation(\"same\") expected error")
	}
}

func TestTagRelationVerdictMapping(t *testing.T) {
	for _, tt := range []struct {
		relation TagRelation
		want     TagVerdict
		decisive bool
	}{
		{TagRelations.SameConcept, TagVerdicts.Same, true},
		{TagRelations.NearSynonym, TagVerdicts.Variant, true},
		{TagRelations.Distinct, TagVerdicts.Related, true},
		{TagRelations.Uncertain, "", false},
	} {
		got, decisive := tt.relation.Verdict()
		if decisive != tt.decisive || got != tt.want {
			t.Errorf("TagRelation(%q).Verdict() = %q, %v; want %q, %v", tt.relation, got, decisive, tt.want, tt.decisive)
		}
	}
}

func TestCanonicalSeparates(t *testing.T) {
	cases := map[string]bool{
		TagDirectionQuery:  true,
		TagDirectionTarget: true,
		TagDirectionTie:    false,
		"":                 false,
		"sideways":         false,
	}
	for canonical, want := range cases {
		if got := CanonicalSeparates(canonical); got != want {
			t.Errorf("CanonicalSeparates(%q) = %v, want %v", canonical, got, want)
		}
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

func TestValidPersonClass(t *testing.T) {
	cases := map[string]bool{
		PersonClassNotPerson:               true,
		PersonClassRealIndividual:          true,
		PersonClassMythologicalOrLegendary: true,
		PersonClassFictional:               true,
		PersonClassUncertain:               true,
		"":                                 false,
		"person":                           false,
		"real":                             false,
		"REAL_INDIVIDUAL":                  false,
	}
	for class, want := range cases {
		if got := ValidPersonClass(class); got != want {
			t.Errorf("ValidPersonClass(%q) = %v, want %v", class, got, want)
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
		Task.Type.TagSplitReask:  true,
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
