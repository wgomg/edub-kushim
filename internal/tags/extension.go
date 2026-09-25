package tags

// AliasResolver resolves a tag name to its canonical (alias-collapsed)
// form; the curation tag service is the production implementation.
type AliasResolver interface {
	Resolve(name string) string
}

// IdentityResolver maps every name to itself; NewExtensionReader uses it
// when no alias resolver is provided.
type IdentityResolver struct{}

// Resolve maps a name to itself.
func (IdentityResolver) Resolve(name string) string { return name }

// Tag is one tag in a snapshot: id, name, and whether it is still
// provisional.
type Tag struct {
	ID          int64
	Name        string
	Provisional bool
}

// Pair is an unordered pair of tag ids; Snapshot records how many
// documents carry both.
type Pair struct {
	A, B int64
}

// Snapshot is an immutable view of the tag vocabulary at an instant: the
// tags (promoted and provisional), their live document masses, and the
// pairwise document overlaps.
type Snapshot struct {
	Tags   []Tag
	Mass   map[int64]int64
	Pairs  map[Pair]int64
	byID   map[int64]Tag
	byName map[string]Tag
}

// NewSnapshot builds a snapshot and its by-id and by-name lookup indexes
// from the given tags.
func NewSnapshot(tags []Tag, mass map[int64]int64, pairs map[Pair]int64) *Snapshot {
	s := &Snapshot{
		Tags:   tags,
		Mass:   mass,
		Pairs:  pairs,
		byID:   make(map[int64]Tag, len(tags)),
		byName: make(map[string]Tag, len(tags)),
	}
	for _, t := range tags {
		s.byID[t.ID] = t
		s.byName[t.Name] = t
	}
	return s
}

// Tag returns the tag with the given id.
func (s *Snapshot) Tag(id int64) (Tag, bool) {
	t, ok := s.byID[id]
	return t, ok
}

// TagByName returns the tag with the given name.
func (s *Snapshot) TagByName(name string) (Tag, bool) {
	t, ok := s.byName[name]
	return t, ok
}

// MassOf returns the tag's live document count, zero when absent.
func (s *Snapshot) MassOf(id int64) int64 { return s.Mass[id] }

// Intersection returns the number of documents both ids are tagged with;
// the diagonal (a == b) is the tag's own mass.
func (s *Snapshot) Intersection(a, b int64) int64 {
	if a == b {
		return s.MassOf(a)
	}
	if a > b {
		a, b = b, a
	}
	return s.Pairs[Pair{A: a, B: b}]
}

// ExtensionReader canonicalizes tag names through the alias resolver
// before readings are recorded, so aliased names collapse onto their
// canonical form in the measurement vocabulary.
type ExtensionReader struct {
	aliases AliasResolver
}

// NewExtensionReader wires the reader; a nil resolver falls back to the
// identity mapping.
func NewExtensionReader(aliases AliasResolver) *ExtensionReader {
	if aliases == nil {
		aliases = IdentityResolver{}
	}
	return &ExtensionReader{aliases: aliases}
}

// Canonical resolves a name through the alias resolver.
func (r *ExtensionReader) Canonical(name string) string { return r.aliases.Resolve(name) }
