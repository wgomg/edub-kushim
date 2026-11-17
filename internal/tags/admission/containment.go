package admission

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/wgomg/edub-kushim/internal/kb"
	"github.com/wgomg/edub-kushim/internal/tags"
)

// ContainmentInput is the containment channel's nomination input: the document's candidate labels and
// the store's known keys in tags.Key form.
type ContainmentInput struct {
	Labels    []string
	StoreKeys map[string]struct{}
}

// kbMaxContainersPerDocument bounds the containment channel's nominations for one document, keeping
// every document's injected set bounded when several labels share broad containers.
const kbMaxContainersPerDocument = 8

// uphillRelTypes are the native relation types whose edge points from a value to its container, in the
// exact form each source stores: OEWN relation names bare, ConceptNet relation URIs with their /r/ prefix.
// The family alone cannot carry the direction: hyponym and mero edges share the broader and part families
// while pointing downhill.
var uphillRelTypes = map[string]struct{}{
	"hypernym":          {},
	"instance_hypernym": {},
	"holo_member":       {},
	"holo_part":         {},
	"holo_substance":    {},
	"/r/IsA":            {},
	"/r/PartOf":         {},
}

// ContainmentCandidates nominates the broader and whole-subject containers of the document's candidate
// labels: their uphill hypernym, instance and holonym neighbours that are not already candidates. A
// container absent from the store is nominated only when an OEWN edge attests it, so the ConceptNet
// IsA blur stays out of vocabulary creation; a store-existing container is nominated from any configured
// source. The result is ordered OEWN attestations first, then by descending weight, capped per document.
// It decides nothing: the nominated names enter the candidate list as ordinary candidates; the quality
// gate owns their about-ness downstream.
func (a *Adjudicator) ContainmentCandidates(ctx context.Context, in ContainmentInput, reqID string) ([]string, error) {
	if a.kb == nil || len(in.Labels) == 0 {
		return nil, nil
	}

	known := make(map[string]struct{}, len(in.Labels))
	for _, label := range in.Labels {
		known[tags.Key(label)] = struct{}{}
	}

	type container struct {
		name    string
		curated bool
		weight  float64
	}

	seen := make(map[string]*container)
	for _, label := range in.Labels {
		edges, err := a.kb.Neighbours(ctx, label, []kb.Family{kb.FamilyBroader, kb.FamilyInstance, kb.FamilyPart})
		if err != nil {
			return nil, fmt.Errorf("kb containment nomination for %q: %w", label, err)
		}
		a.logger.Debug(reqID, "kb containment probe: %q edges=%d", label, len(edges))
		for _, edge := range edges {
			if _, uphill := uphillRelTypes[edge.RelType]; !uphill {
				continue
			}
			for _, lemma := range edge.ToLemmas {
				name := tags.StoredName(lemma)
				key := tags.Key(name)
				if key == "" || key == tags.Key(label) {
					continue
				}
				if _, emitted := known[key]; emitted {
					continue
				}
				c := seen[key]
				if c == nil {
					c = &container{name: name}
					seen[key] = c
				}
				if edge.Source == kb.SourceOEWN {
					c.curated = true
				}
				if edge.Weight != nil && *edge.Weight > c.weight {
					c.weight = *edge.Weight
				}
				a.logger.Debug(reqID, "kb containment: %q -> %q (source=%s reltype=%s)", label, name, edge.Source, edge.RelType)
			}
		}
	}

	containers := make([]*container, 0, len(seen))
	for _, c := range seen {
		if !c.curated {
			if _, inStore := in.StoreKeys[tags.Key(c.name)]; !inStore {
				a.logger.Debug(reqID, "kb containment: skip %q (no OEWN attestation, absent from store)", c.name)
				continue
			}
		}
		containers = append(containers, c)
	}
	slices.SortStableFunc(containers, func(x, y *container) int {
		if x.curated != y.curated {
			if x.curated {
				return -1
			}
			return 1
		}
		if d := cmp.Compare(y.weight, x.weight); d != 0 {
			return d
		}
		return cmp.Compare(x.name, y.name)
	})

	nominated := len(containers)
	if len(containers) > kbMaxContainersPerDocument {
		for _, c := range containers[kbMaxContainersPerDocument:] {
			a.logger.Debug(reqID, "kb containment: drop %q (per-document cap)", c.name)
		}
		containers = containers[:kbMaxContainersPerDocument]
	}
	a.logger.Info(reqID, "kb containment: nominated=%d injected=%d", nominated, len(containers))

	names := make([]string, 0, len(containers))
	for _, c := range containers {
		names = append(names, c.name)
	}
	return names, nil
}
