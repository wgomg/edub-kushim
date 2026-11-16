package kb

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/wgomg/edub-kushim/internal/database"
)

// Reader is the read interface over the installed knowledge base: label resolution, typed neighbours and
// the pairwise probe. Consumers depend on this interface, not on the query implementation. Every call
// answers from the installed sources only: an empty result is a miss, never evidence that a relation does
// not hold.
type Reader interface {
	// Resolve returns every concept whose normalized lemma equals the label's house form, across every
	// installed source.
	Resolve(ctx context.Context, label string) ([]Anchor, error)

	// Neighbours returns the edges of the given families leaving the label's anchors, projected onto the
	// lemma strings both endpoints carry.
	Neighbours(ctx context.Context, label string, families []Family) ([]TypedEdge, error)

	// PairProbe returns the edges directly connecting the two labels' anchors in either direction, plus
	// the shared-concept exact relation when the labels are co-lemmas of one concept.
	PairProbe(ctx context.Context, labelA, labelB string, families []Family) ([]TypedEdge, error)
}

// resolveAnchor is an Anchor plus the installed source version, kept internal so the public Anchor stays
// the four caller-facing fields while derived edges still carry their version.
type resolveAnchor struct {
	Anchor
	version string
}

// conceptRef identifies one concept within one source.
type conceptRef struct {
	source string
	key    string
}

// Resolve returns every concept whose normalized lemma equals the label's house form, across all installed
// sources, ordered by source and concept key. It returns an empty result when nothing matches — an absent
// source and an unknown label are the same answer.
func (s *Store) Resolve(ctx context.Context, label string) ([]Anchor, error) {
	anchors, err := resolve(ctx, s.client.Queries, label)
	if err != nil {
		return nil, err
	}

	out := make([]Anchor, 0, len(anchors))
	for _, a := range anchors {
		out = append(out, a.Anchor)
	}
	return out, nil
}

func resolve(ctx context.Context, q *database.Queries, label string) ([]resolveAnchor, error) {
	form := lemmaForm(label)
	if form == "" {
		return nil, nil
	}

	rows, err := q.KBConceptsForLemma(ctx, form)
	if err != nil {
		return nil, fmt.Errorf("resolve kb label %q: %w", label, err)
	}

	anchors := make([]resolveAnchor, 0, len(rows))
	for _, r := range rows {
		anchors = append(anchors, resolveAnchor{
			Anchor: Anchor{
				Source:     r.Source,
				ConceptKey: r.ConceptKey,
				Pos:        r.Pos.String,
				Gloss:      r.Gloss.String,
			},
			version: r.SourceVersion,
		})
	}
	return anchors, nil
}

// Neighbours returns the edges of the given families leaving every anchor of the label, projected onto the
// lemma strings both endpoints carry. Exact synonymy is derived from the co-lemmas of a shared concept
// (OEWN materializes no synonym edges), so a source that materializes its own exact edges is unaffected:
// its unsensed nodes carry one lemma each. An empty result is a miss, not evidence against the relation.
func (s *Store) Neighbours(ctx context.Context, label string, families []Family) ([]TypedEdge, error) {
	fams, err := familyStrings(families)
	if err != nil {
		return nil, err
	}

	anchors, err := resolve(ctx, s.client.Queries, label)
	if err != nil {
		return nil, err
	}
	if len(anchors) == 0 {
		return nil, nil
	}

	q := s.client.Queries
	sources, keys := anchorArrays(anchors)
	rows, err := q.KBEdgesFromConcepts(ctx, database.KBEdgesFromConceptsParams{
		Sources:     sources,
		ConceptKeys: keys,
		Families:    fams,
	})
	if err != nil {
		return nil, fmt.Errorf("read kb neighbours of %q: %w", label, err)
	}

	refs := make([]conceptRef, 0, len(rows)*2+len(anchors))
	for _, a := range anchors {
		refs = append(refs, conceptRef{source: a.Source, key: a.ConceptKey})
	}
	for _, r := range rows {
		refs = append(refs, conceptRef{source: r.Source, key: r.FromConceptKey})
		refs = append(refs, conceptRef{source: r.Source, key: r.ToConceptKey})
	}
	lemmas, err := lemmasFor(ctx, q, refs)
	if err != nil {
		return nil, err
	}

	views := make([]edgeView, 0, len(rows))
	for _, r := range rows {
		views = append(views, edgeView{
			source:        r.Source,
			sourceVersion: r.SourceVersion,
			fromKey:       r.FromConceptKey,
			toKey:         r.ToConceptKey,
			family:        r.Family,
			reltype:       r.SourceReltype,
			weight:        r.Weight,
		})
	}
	edges := typedEdges(views, lemmas)

	if slices.Contains(families, FamilyExact) {
		edges = append(edges, coLemmaEdges(anchors, lemmas, lemmaForm(label))...)
	}
	return edges, nil
}

// PairProbe returns the edges directly connecting either label's anchors to the other's, in both directions,
// plus the shared-concept exact relation when the two labels are co-lemmas of one concept. It is the
// stage-c guard and prior lookup: a miss means the sources assert nothing about the pair.
func (s *Store) PairProbe(ctx context.Context, labelA, labelB string, families []Family) ([]TypedEdge, error) {
	fams, err := familyStrings(families)
	if err != nil {
		return nil, err
	}

	a, err := resolve(ctx, s.client.Queries, labelA)
	if err != nil {
		return nil, err
	}
	b, err := resolve(ctx, s.client.Queries, labelB)
	if err != nil {
		return nil, err
	}
	if len(a) == 0 || len(b) == 0 {
		return nil, nil
	}

	q := s.client.Queries
	aSources, aKeys := anchorArrays(a)
	bSources, bKeys := anchorArrays(b)
	rows, err := q.KBEdgesBetweenConcepts(ctx, database.KBEdgesBetweenConceptsParams{
		ASources: aSources,
		AKeys:    aKeys,
		BSources: bSources,
		BKeys:    bKeys,
		Families: fams,
	})
	if err != nil {
		return nil, fmt.Errorf("probe kb pair %q/%q: %w", labelA, labelB, err)
	}

	refs := make([]conceptRef, 0, len(rows)*2+len(a)+len(b))
	for _, anchor := range a {
		refs = append(refs, conceptRef{source: anchor.Source, key: anchor.ConceptKey})
	}
	for _, anchor := range b {
		refs = append(refs, conceptRef{source: anchor.Source, key: anchor.ConceptKey})
	}
	for _, r := range rows {
		refs = append(refs, conceptRef{source: r.Source, key: r.FromConceptKey})
		refs = append(refs, conceptRef{source: r.Source, key: r.ToConceptKey})
	}
	lemmas, err := lemmasFor(ctx, q, refs)
	if err != nil {
		return nil, err
	}

	views := make([]edgeView, 0, len(rows))
	for _, r := range rows {
		views = append(views, edgeView{
			source:        r.Source,
			sourceVersion: r.SourceVersion,
			fromKey:       r.FromConceptKey,
			toKey:         r.ToConceptKey,
			family:        r.Family,
			reltype:       r.SourceReltype,
			weight:        r.Weight,
		})
	}
	edges := typedEdges(views, lemmas)

	if slices.Contains(families, FamilyExact) {
		edges = append(edges, sharedConceptExact(lemmaForm(labelA), lemmaForm(labelB), a, b)...)
	}
	return edges, nil
}

// edgeView is one database edge row reduced to the fields both edge queries share. The generated row types
// differ per query, so each path converts into this view and the TypedEdge projection exists once.
type edgeView struct {
	source        string
	sourceVersion string
	fromKey       string
	toKey         string
	family        string
	reltype       string
	weight        sql.NullFloat64
}

// typedEdges projects edge rows onto TypedEdge and resolves both endpoints' lemma strings. It is the single
// place the shared edge shape is built, so Neighbours and PairProbe cannot drift apart.
func typedEdges(views []edgeView, lemmas map[conceptRef][]string) []TypedEdge {
	edges := make([]TypedEdge, 0, len(views))
	for _, view := range views {
		edges = append(edges, TypedEdge{
			Source:        view.source,
			SourceVersion: view.sourceVersion,
			Family:        Family(view.family),
			RelType:       view.reltype,
			Weight:        nullWeight(view.weight),
			FromKey:       view.fromKey,
			ToKey:         view.toKey,
			FromLemmas:    lemmas[conceptRef{source: view.source, key: view.fromKey}],
			ToLemmas:      lemmas[conceptRef{source: view.source, key: view.toKey}],
		})
	}
	return edges
}

// coLemmaEdges expands every anchor's co-lemmas into derived exact edges. The label's own form is excluded,
// so the result only carries the other synonyms of each resolved sense.
func coLemmaEdges(anchors []resolveAnchor, lemmas map[conceptRef][]string, form string) []TypedEdge {
	var edges []TypedEdge
	for _, a := range anchors {
		ref := conceptRef{source: a.Source, key: a.ConceptKey}
		for _, lemma := range lemmas[ref] {
			if lemma == form {
				continue
			}
			edges = append(edges, TypedEdge{
				Source:        a.Source,
				SourceVersion: a.version,
				Family:        FamilyExact,
				RelType:       coLemmaRelType,
				FromKey:       a.ConceptKey,
				ToKey:         a.ConceptKey,
				FromLemmas:    []string{form},
				ToLemmas:      []string{lemma},
			})
		}
	}
	return edges
}

// sharedConceptExact returns one derived exact edge per concept both labels resolve to.
func sharedConceptExact(formA, formB string, a, b []resolveAnchor) []TypedEdge {
	shared := make(map[conceptRef]string, len(a))
	for _, anchor := range a {
		shared[conceptRef{source: anchor.Source, key: anchor.ConceptKey}] = anchor.version
	}

	var edges []TypedEdge
	for _, anchor := range b {
		ref := conceptRef{source: anchor.Source, key: anchor.ConceptKey}
		version, ok := shared[ref]
		if !ok {
			continue
		}
		edges = append(edges, TypedEdge{
			Source:        anchor.Source,
			SourceVersion: version,
			Family:        FamilyExact,
			RelType:       coLemmaRelType,
			FromKey:       anchor.ConceptKey,
			ToKey:         anchor.ConceptKey,
			FromLemmas:    []string{formA},
			ToLemmas:      []string{formB},
		})
	}
	return edges
}

// lemmasFor loads the lemma strings of every distinct concept reference in refs. Missing refs simply carry
// no entry; an edge whose endpoint has no lemma row projects to an empty list.
func lemmasFor(ctx context.Context, q *database.Queries, refs []conceptRef) (map[conceptRef][]string, error) {
	seen := make(map[conceptRef]struct{}, len(refs))
	sources := make([]string, 0, len(refs))
	keys := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.source == "" || ref.key == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		sources = append(sources, ref.source)
		keys = append(keys, ref.key)
	}
	if len(sources) == 0 {
		return nil, nil
	}

	rows, err := q.KBLemmasForConcepts(ctx, database.KBLemmasForConceptsParams{
		Sources:     sources,
		ConceptKeys: keys,
	})
	if err != nil {
		return nil, fmt.Errorf("read kb lemmas: %w", err)
	}

	lemmas := make(map[conceptRef][]string, len(rows))
	for _, r := range rows {
		ref := conceptRef{source: r.Source, key: r.ConceptKey}
		lemmas[ref] = append(lemmas[ref], r.LemmaNorm)
	}
	return lemmas, nil
}

func anchorArrays(anchors []resolveAnchor) ([]string, []string) {
	sources := make([]string, 0, len(anchors))
	keys := make([]string, 0, len(anchors))
	for _, a := range anchors {
		sources = append(sources, a.Source)
		keys = append(keys, a.ConceptKey)
	}
	return sources, keys
}

func familyStrings(families []Family) ([]string, error) {
	out := make([]string, 0, len(families))
	for _, f := range families {
		if _, ok := validFamilies[f]; !ok {
			return nil, fmt.Errorf("unknown kb family %q", f)
		}
		out = append(out, string(f))
	}
	return out, nil
}

func nullWeight(w sql.NullFloat64) *float64 {
	if !w.Valid {
		return nil
	}
	weight := w.Float64
	return &weight
}
