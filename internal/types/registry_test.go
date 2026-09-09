package types

import "testing"

func TestAllCounts(t *testing.T) {
	if got, want := len(AllBatchSources()), 10; got != want {
		t.Errorf("AllBatchSources len = %d, want %d", got, want)
	}
	if got, want := len(AllBatchStatuses()), 6; got != want {
		t.Errorf("AllBatchStatuses len = %d, want %d", got, want)
	}
	if got, want := len(AllTaskTypes()), 6; got != want {
		t.Errorf("AllTaskTypes len = %d, want %d", got, want)
	}
	if got, want := len(AllTaskStatuses()), 7; got != want {
		t.Errorf("AllTaskStatuses len = %d, want %d", got, want)
	}
	if got, want := len(NonConsumeBatchSources()), 3; got != want {
		t.Errorf("NonConsumeBatchSources len = %d, want %d", got, want)
	}
}

func TestValid(t *testing.T) {
	batchCases := map[string]bool{
		string(Batch.Source.CLI):           true,
		string(Batch.Source.Upload):        true,
		string(Batch.Source.Thumbbackfill): true,
		"test":                             false,
		"unknown":                          false,
	}
	for s, want := range batchCases {
		if got := BatchSource(s).Valid(); got != want {
			t.Errorf("BatchSource(%q).Valid() = %v, want %v", s, got, want)
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
	for _, src := range AllBatchSources() {
		got, err := ParseBatchSource(string(src))
		if err != nil {
			t.Errorf("ParseBatchSource(%q) error: %v", src, err)
		}
		if got != src {
			t.Errorf("ParseBatchSource round-trip mismatch: got %q, want %q", got, src)
		}
	}
	for _, st := range AllTaskStatuses() {
		got, err := ParseTaskStatus(string(st))
		if err != nil {
			t.Errorf("ParseTaskStatus(%q) error: %v", st, err)
		}
		if got != st {
			t.Errorf("ParseTaskStatus round-trip mismatch: got %q, want %q", got, st)
		}
	}
	if _, err := ParseBatchSource("test"); err == nil {
		t.Error("ParseBatchSource(\"test\") expected error")
	}
	if _, err := ParseTaskStatus("active"); err == nil {
		t.Error("ParseTaskStatus(\"active\") expected error")
	}
}

func TestAllReturnsClone(t *testing.T) {
	a := AllBatchSources()
	b := AllBatchSources()
	if &a[0] == &b[0] {
		t.Error("AllBatchSources returned the same backing slice twice")
	}
	a[0] = BatchSource("mutated")
	if AllBatchSources()[0] == BatchSource("mutated") {
		t.Error("AllBatchSources did not return an independent clone")
	}
}

func TestNonConsumeBatchSources(t *testing.T) {
	for _, src := range NonConsumeBatchSources() {
		if src != Batch.Source.Config && src != Batch.Source.Backup && src != Batch.Source.Mirror {
			t.Errorf("unexpected non-consume source: %q", src)
		}
	}
}

func TestIsBlocking(t *testing.T) {
	cases := map[TaskType]bool{
		Task.Type.Backup: true,
		Task.Type.Mirror: true,
		Task.Type.Config: false,
	}
	for typ, want := range cases {
		if got := typ.IsBlocking(); got != want {
			t.Errorf("TaskType(%q).IsBlocking() = %v, want %v", typ, got, want)
		}
	}
}

func TestIsLockGated(t *testing.T) {
	cases := map[TaskType]bool{
		Task.Type.Consume:   true,
		Task.Type.Enrich:    true,
		Task.Type.Thumbnail: true,
		Task.Type.Config:    false,
	}
	for typ, want := range cases {
		if got := typ.IsLockGated(); got != want {
			t.Errorf("TaskType(%q).IsLockGated() = %v, want %v", typ, got, want)
		}
	}
}
