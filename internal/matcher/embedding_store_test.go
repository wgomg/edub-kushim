package matcher

import (
	"slices"
	"testing"
)

// The daemon constructs the store with a nil map at startup, so the
// constructor's nil handling must leave a map Add can write to.
func TestTagEmbeddingStore_NilEntriesStartsUsable(t *testing.T) {
	store := NewTagEmbeddingStore(nil)

	store.Add("alpha", []float32{1, 2})
	store.Add("alpha", []float32{3, 4})
	if got := store.Entries()["alpha"]; !slices.Equal(got, []float32{3, 4}) {
		t.Errorf("Entries[alpha] after re-Add = %v, want [3 4]", got)
	}

	store.Remove("alpha")
	if got := store.Entries(); len(got) != 0 {
		t.Errorf("Entries after Remove = %v, want empty", got)
	}
}

// Readers (Match, rebuildPairs) iterate Entries while Add and Remove run, so
// the returned map and its vectors must be copies: a caller mutating them
// must not corrupt the store matching reads from.
func TestTagEmbeddingStore_EntriesAreDeepCopies(t *testing.T) {
	store := NewTagEmbeddingStore(map[string][]float32{"alpha": {1, 2}})

	got := store.Entries()
	got["alpha"][0] = 99
	got["beta"] = []float32{7}

	want := store.Entries()
	if !slices.Equal(want["alpha"], []float32{1, 2}) {
		t.Errorf("mutating the Entries copy changed stored alpha: %v, want [1 2]", want["alpha"])
	}
	if len(want) != 1 {
		t.Errorf("mutating the Entries copy changed the store: %v, want only the alpha entry", want)
	}
}
