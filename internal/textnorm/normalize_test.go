package textnorm

import (
	"sync"
	"testing"
)

func TestNormalizeForDB(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"ascii lowercase", "jose garcia", "jose garcia"},
		{"uppercase", "JOSE GARCIA", "jose garcia"},
		{"accent folding", "José García", "jose garcia"},
		{"tilde", "niño", "nino"},
		{"umlaut", "Müller", "muller"},
		{"cedilla", "François", "francois"},
		{"hyphen to space", "maria-jose", "maria jose"},
		{"underscore to space", "maria_jose", "maria jose"},
		{"strip punctuation", "O'Brien", "obrien"},
		{"strip quotes", `"Smith"`, "smith"},
		{"strip brackets", "Lee [Jr]", "lee jr"},
		{"collapse spaces", "  jose   garcia  ", "jose garcia"},
		{"empty string", "", ""},
		{"only spaces", "   ", ""},
		{"only punctuation", "...", ""},
		{"mixed unicode", "Ñoño", "nono"},
		{"greek letters", "αβγ", ""},
		{"numbers stripped", "author123", "author"},
		{"preserve internal spaces", "van der berg", "van der berg"},
		{"em dash to space", "smith\u2014jones", "smith jones"},
		{"single char", "A", "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeForDB(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeForDB(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// Regression guard for the shared-transform.Chain data race: NormalizeForDB
// is called from concurrent enrich workers and HTTP handlers, and a shared
// stateful chain panicked with "slice bounds out of range" under overlap.
// Run with -race; each goroutine also pins exact output so corruption (not
// just a panic) fails the test.
func TestNormalizeForDB_Concurrent(t *testing.T) {
	inputs := []string{"José García", "Müller", "Ñoño", "François", "maria-jose"}
	wants := []string{"jose garcia", "muller", "nono", "francois", "maria jose"}

	const goroutines = 8
	const iterations = 1000

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				for j, in := range inputs {
					if got := NormalizeForDB(in); got != wants[j] {
						t.Errorf("NormalizeForDB(%q) = %q, want %q", in, got, wants[j])
						return
					}
				}
			}
		})
	}
	wg.Wait()
}
