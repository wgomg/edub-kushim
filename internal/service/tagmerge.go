package service

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/tools/adapters/tagmatcher"
	"github.com/wgomg/edub-kushim/internal/utils"
)

const (
	mergeSuggestionConfidencePopulation     = "population"
	mergeSuggestionConfidenceSimilarityOnly = "similarity_only"
)

type MergeTagResult struct {
	SourceID   int64
	TargetID   int64
	TargetName string
	Reassigned int64
}

type MergeSuggestionTag struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	DocumentCount int64  `json:"document_count"`
}

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

type SuggestionsParams struct {
	Limit            int
	ForTag           string
	IncludeDismissed bool
}

type TagMerge struct {
	client   *database.Client
	matcher  tagmatcher.Matcher
	embedder tagmatcher.Embedder
	logger   *utils.Logger
}

func NewTagMerge(client *database.Client, matcher tagmatcher.Matcher, embedder tagmatcher.Embedder, logger *utils.Logger) *TagMerge {
	return &TagMerge{client: client, matcher: matcher, embedder: embedder, logger: logger}
}

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
		Name:       utils.NormalizeTag(source.Name),
		Kind:       DeletedTagKindMerged,
		TargetName: sql.NullString{String: utils.NormalizeTag(target.Name), Valid: true},
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

func (s *TagMerge) Suggestions(ctx context.Context, params SuggestionsParams) ([]MergeSuggestion, error) {
	pairs, err := s.matcher.SimilarPairs(ctx)
	if err != nil {
		return nil, err
	}

	tags, err := s.client.Queries.ListAllTagsWithDocumentCount(ctx)
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

	forTag := utils.NormalizeTag(params.ForTag)

	tagByName := make(map[string]database.Tag, len(tags))
	countByID := make(map[int64]int64, len(tags))
	for _, t := range tags {
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
	stats, err := s.client.Queries.TagPairEvidenceStats(ctx, database.TagPairEvidenceStatsParams{
		Queries: queries,
		Targets: targets,
	})
	if err != nil {
		return nil, errs.FromDB(err, "tag pair evidence stats")
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
		ev.eventCount += row.EventCount
		ev.documentOverlap += row.DocumentCount
		ev.sameCount += row.SameCount
		ev.variantCount += row.VariantCount
		ev.relatedCount += row.RelatedCount
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
	a := utils.NormalizeTag(tagA)
	b := utils.NormalizeTag(tagB)
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
