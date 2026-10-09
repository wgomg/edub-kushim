package tags

import (
	_ "embed"
	"strconv"
	"strings"
	"sync"
)

// attestedFoldsData is the generated adjective→noun attestation table the split seam folds
// through (docs/tags/canonical-form-and-identity.md J1): the pertainym relation of WordNet's
// relational adjectives, extracted by internal/tags/gen_attested_folds.go. Each non-comment line
// is one `adjective noun` pair; the header carries the source and the WordNet 3.0 notice.
//
//go:embed attested_folds.txt
var attestedFoldsData string

// attestedFolds parses the embedded asset once into the adjective→bases index. Blank and comment lines are
// skipped; a malformed pair fails loudly rather than yielding a partial table, since the asset is generated
// at build time and a bad line means the checked-in data is corrupt.
var attestedFolds = sync.OnceValue(func() map[string][]string {
	table := make(map[string][]string)
	for line := range strings.SplitSeq(attestedFoldsData, "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		adjective, noun, ok := strings.Cut(line, " ")
		if !ok || adjective == "" || noun == "" {
			panic("tags: malformed attested fold line: " + strconv.Quote(line))
		}
		table[adjective] = append(table[adjective], noun)
	}
	return table
})

// attestedBases returns the noun bases the attestation table relates to a single-token name, nil
// when the table does not attest the name or the name is not one token. A name with multiple bases
// is sense ambiguity the caller resolves against the store.
func attestedBases(name string) []string {
	if len(strings.Fields(name)) != 1 {
		return nil
	}
	return attestedFolds()[strings.ToLower(name)]
}
