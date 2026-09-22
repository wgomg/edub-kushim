package tags

type AliasResolver interface {
	Resolve(name string) string
}

type IdentityResolver struct{}

func (IdentityResolver) Resolve(name string) string { return name }

type Tag struct {
	ID          int64
	Name        string
	Provisional bool
}

type Pair struct {
	A, B int64
}

type Snapshot struct {
	Tags   []Tag
	Mass   map[int64]int64
	Pairs  map[Pair]int64
	byID   map[int64]Tag
	byName map[string]Tag
}

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

func (s *Snapshot) Tag(id int64) (Tag, bool) {
	t, ok := s.byID[id]
	return t, ok
}

func (s *Snapshot) TagByName(name string) (Tag, bool) {
	t, ok := s.byName[name]
	return t, ok
}

func (s *Snapshot) MassOf(id int64) int64 { return s.Mass[id] }

func (s *Snapshot) Intersection(a, b int64) int64 {
	if a == b {
		return s.MassOf(a)
	}
	if a > b {
		a, b = b, a
	}
	return s.Pairs[Pair{A: a, B: b}]
}

type ExtensionReader struct {
	aliases AliasResolver
}

func NewExtensionReader(aliases AliasResolver) *ExtensionReader {
	if aliases == nil {
		aliases = IdentityResolver{}
	}
	return &ExtensionReader{aliases: aliases}
}

func (r *ExtensionReader) Canonical(name string) string { return r.aliases.Resolve(name) }
