package curation

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/textnorm"
)

const (
	mergeSuggestionConfidencePopulation     = "population"
	mergeSuggestionConfidenceSimilarityOnly = "similarity_only"
)

// MergeTagResult reports a completed merge: the source id that vanished,
// the target it merged into, and how many documents were reassigned.
type MergeTagResult struct {
	SourceID   int64
	TargetID   int64
	TargetName string
	Reassigned int64
}

// MergeSuggestionTag is one endpoint of a suggested pair: id, name and
// live document count.
type MergeSuggestionTag struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	DocumentCount int64  `json:"document_count"`
}

// MergeSuggestion is one merge-review candidate: the two tags, their
// embedding similarity, the verdict-event evidence recorded between them,
// and whether the pair was dismissed. Confidence says whether the pair is
// backed by a recorded verdict population or by similarity alone.
type MergeSuggestion struct {
	Tags            []MergeSuggestionTag `json:"tags"`
	Similarity      float64              `json:"similarity"`
	EventCount      int64                `json:"event_count"`
	DocumentOverlap int64                `json:"document_overlap"`
	SameCount       int64                `json:"same_count"`
	VariantCount    int64                `json:"variant_count"`
	RelatedCount    int64                `json:"related_count"`
	Confidence      string               `json:"confidence"`
	Dismissed       bool                 `json:"dismissed,omitempty"`
}

// SuggestionsParams scopes the merge suggestion listing: page size, an
// optional focus tag, and whether dismissed pairs come back.
type SuggestionsParams struct {
	Limit            int
	ForTag           string
	IncludeDismissed bool
}

// TagMerge executes merge review: the merge itself, the suggestion listing
// over matcher pairs and verdict evidence, and the dismissal bookkeeping.
type TagMerge struct {
	client   *database.Client
	matcher  matcher.Matcher
	embedder matcher.Embedder
	logger   *log.Logger
}

// NewTagMerge wires the merge service; matcher supplies the similar pairs
// and embedder keeps the vocabulary in sync after merges.
func NewTagMerge(client *database.Client, matcher matcher.Matcher, embedder matcher.Embedder, logger *log.Logger) *TagMerge {
	return &TagMerge{client: client, matcher: matcher, embedder: embedder, logger: logger}
}

// Merge folds the source tag into the target inside one transaction:
// documents are reassigned, orphan marks cleared, the source row deleted,
// and the merge recorded in the deleted-tag blocklist (kind merged) so the
// name cannot silently come back. The source's name leaves the matcher
// store after commit.
func (s *TagMerge) Merge(ctx context.Context, sourceID, targetID int64) (MergeTagResult, error) {
	if sourceID == targetID {
		return MergeTagResult{}, errs.EInvalid("merge tag", errors.New("source and target must differ"))
	}

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return MergeTagResult{}, errs.FromDB(err, "begin transaction for tag merge")
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)

	// FOR UPDATE: document_tag.tag_id is ON DELETE CASCADE, so an unlocked
	// concurrent delete of the target would silently destroy the rows just
	// re-pointed. Ascending id order keeps opposite-direction merges from deadlocking.
	var source, target database.Tag
	if sourceID < targetID {
		source, err = txQ.GetTagForUpdate(ctx, sourceID)
		if err != nil {
			return MergeTagResult{}, errs.FromDB(err, "get source tag")
		}
		target, err = txQ.GetTagForUpdate(ctx, targetID)
		if err != nil {
			return MergeTagResult{}, errs.FromDB(err, "get target tag")
		}
	} else {
		target, err = txQ.GetTagForUpdate(ctx, targetID)
		if err != nil {
			return MergeTagResult{}, errs.FromDB(err, "get target tag")
		}
		source, err = txQ.GetTagForUpdate(ctx, sourceID)
		if err != nil {
			return MergeTagResult{}, errs.FromDB(err, "get source tag")
		}
	}

	reassigned, err := txQ.ReassignDocumentTags(ctx, database.ReassignDocumentTagsParams{
		SourceID: sourceID,
		TargetID: targetID,
	})
	if err != nil {
		return MergeTagResult{}, errs.FromDB(err, "reassign document tags")
	}

	if reassigned > 0 {
		if err := txQ.ClearOrphanTagMarksByTagIDs(ctx, []int64{targetID}); err != nil {
			return MergeTagResult{}, errs.FromDB(err, "clear orphan tag marks")
		}
	}

	if _, err := txQ.DeleteDocumentTagsForTag(ctx, sourceID); err != nil {
		return MergeTagResult{}, errs.FromDB(err, "delete leftover document tags")
	}

	if err := txQ.DeleteTag(ctx, sourceID); err != nil {
		return MergeTagResult{}, errs.FromDB(err, "delete source tag")
	}

	if err := txQ.UpsertDeletedTag(ctx, database.UpsertDeletedTagParams{
		Name:       textnorm.NormalizeTag(source.Name),
		Kind:       DeletedTagKindMerged,
		TargetName: sql.NullString{String: textnorm.NormalizeTag(target.Name), Valid: true},
	}); err != nil {
		return MergeTagResult{}, errs.FromDB(err, "record merged tag")
	}

	if err := tx.Commit(); err != nil {
		return MergeTagResult{}, errs.FromDB(err, "commit tag merge")
	}

	if err := s.embedder.RemoveFromStore(ctx, []string{source.Name}); err != nil {
		s.logger.Error(nil, "tag merge: remove from store: %v", err)
	}

	return MergeTagResult{
		SourceID:   sourceID,
		TargetID:   targetID,
		TargetName: target.Name,
		Reassigned: reassigned,
	}, nil
}

type pairEvidence struct {
	a, b            string
	similarity      float64
	eventCount      int64
	documentOverlap int64
	sameCount       int64
	variantCount    int64
	relatedCount    int64
}

// Suggestions lists the merge-review candidates from the matcher's
// similar pairs, annotated with verdict evidence and confidence, ordered
// with population-backed pairs first. Dismissed pairs stay hidden unless
// asked, and each class keeps its own share of the page limit.
func (s *TagMerge) Suggestions(ctx context.Context, params SuggestionsParams) ([]MergeSuggestion, error) {
	pairs, err := s.matcher.SimilarPairs(ctx)
	if err != nil {
		return nil, err
	}

	allTags, err := s.client.Queries.ListAllTagsWithDocumentCount(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list all tags with document counts")
	}

	dismissals, err := s.client.Queries.ListTagMergeDismissals(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list tag merge dismissals")
	}
	dismissed := make(map[string]struct{}, len(dismissals))
	for _, d := range dismissals {
		dismissed[pairKey(d.TagA, d.TagB)] = struct{}{}
	}

	forTag := textnorm.NormalizeTag(params.ForTag)

	tagByName := make(map[string]database.Tag, len(allTags))
	countByID := make(map[int64]int64, len(allTags))
	for _, t := range allTags {
		tagByName[t.Name] = database.Tag{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt}
		countByID[t.ID] = t.DocumentCount
	}

	pairEv := make(map[string]*pairEvidence, len(pairs))
	for _, p := range pairs {
		if _, ok := tagByName[p.A]; !ok {
			continue
		}
		if _, ok := tagByName[p.B]; !ok {
			continue
		}
		key := pairKey(p.A, p.B)
		ev := pairEv[key]
		if ev == nil {
			ev = &pairEvidence{a: p.A, b: p.B, similarity: p.Similarity}
			pairEv[key] = ev
		} else if p.Similarity > ev.similarity {
			ev.similarity = p.Similarity
		}
	}

	queries := make([]string, 0, len(pairEv)*2)
	targets := make([]string, 0, len(pairEv)*2)
	for _, ev := range pairEv {
		queries = append(queries, ev.a, ev.b)
		targets = append(targets, ev.b, ev.a)
	}
	evidencePairs := make([]tags.PairNames, 0, len(queries))
	for i := range queries {
		evidencePairs = append(evidencePairs, tags.PairNames{Query: queries[i], Target: targets[i]})
	}
	stats, err := tags.NewStore(s.client.Queries).PairVerdictStats(ctx, evidencePairs)
	if err != nil {
		return nil, err
	}
	for _, row := range stats {
		if _, ok := tagByName[row.Query]; !ok {
			continue
		}
		if _, ok := tagByName[row.Target]; !ok {
			continue
		}
		ev, ok := pairEv[pairKey(row.Query, row.Target)]
		if !ok {
			continue
		}
		ev.eventCount += row.Events
		ev.documentOverlap += row.Documents
		ev.sameCount += row.Same
		ev.variantCount += row.Variant
		ev.relatedCount += row.Related
	}

	suggestions := make([]MergeSuggestion, 0, len(pairEv))
	for _, ev := range pairEv {
		if forTag != "" && ev.a != forTag && ev.b != forTag {
			continue
		}
		_, isDismissed := dismissed[pairKey(ev.a, ev.b)]
		a := tagByName[ev.a]
		b := tagByName[ev.b]
		ta := MergeSuggestionTag{ID: a.ID, Name: a.Name, DocumentCount: countByID[a.ID]}
		tb := MergeSuggestionTag{ID: b.ID, Name: b.Name, DocumentCount: countByID[b.ID]}
		if ta.DocumentCount < tb.DocumentCount ||
			(ta.DocumentCount == tb.DocumentCount && ta.Name > tb.Name) {
			ta, tb = tb, ta
		}
		confidence := mergeSuggestionConfidenceSimilarityOnly
		if ev.eventCount > 0 {
			confidence = mergeSuggestionConfidencePopulation
		}
		suggestions = append(suggestions, MergeSuggestion{
			Tags:            []MergeSuggestionTag{ta, tb},
			Similarity:      ev.similarity,
			EventCount:      ev.eventCount,
			DocumentOverlap: ev.documentOverlap,
			SameCount:       ev.sameCount,
			VariantCount:    ev.variantCount,
			RelatedCount:    ev.relatedCount,
			Confidence:      confidence,
			Dismissed:       isDismissed,
		})
	}

	slices.SortStableFunc(suggestions, func(a, b MergeSuggestion) int {
		ap := a.Confidence == mergeSuggestionConfidencePopulation
		bp := b.Confidence == mergeSuggestionConfidencePopulation
		if ap != bp {
			if ap {
				return -1
			}
			return 1
		}
		if ap {
			av := a.VariantCount + a.RelatedCount
			bv := b.VariantCount + b.RelatedCount
			if av != bv {
				if av > bv {
					return -1
				}
				return 1
			}
			if a.DocumentOverlap != b.DocumentOverlap {
				if a.DocumentOverlap > b.DocumentOverlap {
					return -1
				}
				return 1
			}
		}
		if a.Similarity != b.Similarity {
			if a.Similarity > b.Similarity {
				return -1
			}
			return 1
		}
		return 0
	})

	// each class gets its own Limit share so dismissed pairs can't starve the live queue
	kept := make([]MergeSuggestion, 0, len(suggestions))
	pendingKept, dismissedKept := 0, 0
	for _, s := range suggestions {
		if s.Dismissed {
			if !params.IncludeDismissed {
				continue
			}
			if dismissedKept >= params.Limit {
				continue
			}
			dismissedKept++
		} else {
			if pendingKept >= params.Limit {
				continue
			}
			pendingKept++
		}
		kept = append(kept, s)
	}
	return kept, nil
}

// DismissPair records the decision not to merge a pair; the names are
// normalized and ordered, and both tags must exist.
func (s *TagMerge) DismissPair(ctx context.Context, tagA, tagB string) error {
	a, b, err := canonicalMergePair("dismiss merge pair", tagA, tagB)
	if err != nil {
		return err
	}
	if _, err := s.client.Queries.GetTagByName(ctx, a); err != nil {
		return errs.FromDB(err, "get tag a for dismissal")
	}
	if _, err := s.client.Queries.GetTagByName(ctx, b); err != nil {
		return errs.FromDB(err, "get tag b for dismissal")
	}
	if err := s.client.Queries.UpsertTagMergeDismissal(ctx, database.UpsertTagMergeDismissalParams{
		TagA: a,
		TagB: b,
	}); err != nil {
		return errs.FromDB(err, "dismiss merge pair")
	}
	return nil
}

// RestorePair removes a pair's dismissal so it can be reviewed again.
func (s *TagMerge) RestorePair(ctx context.Context, tagA, tagB string) error {
	a, b, err := canonicalMergePair("restore merge pair", tagA, tagB)
	if err != nil {
		return err
	}
	if _, err := s.client.Queries.DeleteTagMergeDismissal(ctx, database.DeleteTagMergeDismissalParams{
		TagA: a,
		TagB: b,
	}); err != nil {
		return errs.FromDB(err, "restore merge pair")
	}
	return nil
}

func canonicalMergePair(op, tagA, tagB string) (string, string, error) {
	a := textnorm.NormalizeTag(tagA)
	b := textnorm.NormalizeTag(tagB)
	if a == "" || b == "" {
		return "", "", errs.EInvalid(op, errors.New("tag names are required"))
	}
	if a == b {
		return "", "", errs.EInvalid(op, errors.New("tag names must differ"))
	}
	if a > b {
		a, b = b, a
	}
	return a, b, nil
}

func pairKey(a, b string) string {
	if a < b {
		return a + "\x00" + b
	}
	return b + "\x00" + a
}
