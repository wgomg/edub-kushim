// Package curation is the tag store's human-facing operations: reading and
// searching the vocabulary, creating tags through the deterministic
// identity plan, renaming, deleting with blocklist bookkeeping, promoting
// provisional tags, managing deleted-tag records, and keeping the matcher's
// embedding store in sync with the tags table. It is the single owner of
// the tag-store invariants; the pipeline side applies its writes through
// this package. The merge-review flows are documented in
// docs/tags/consolidation.md.
package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/matcher"
	"github.com/wgomg/edub-kushim/internal/service"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/textnorm"
)

// TagUpdatePair is one rename request: the tag's id and its new name.
type TagUpdatePair struct {
	ID   int64
	Name string
}

// The deleted-tag kinds: blocked (deleted by an operator) and merged
// (folded into a surviving target). DocumentTagSourceEnrich marks document
// tag rows created by the enrichment pipeline.
const (
	DeletedTagKindBlocked = "blocked"
	DeletedTagKindMerged  = "merged"

	DocumentTagSourceEnrich = "enrich"
)

// Tag is the tag store's read, write and administration surface: CRUD,
// planned registrations, aliases, deleted tags, promotion, and the sync
// that keeps the matcher's embedding store aligned with the tags table.
type Tag struct {
	client   *database.Client
	embedder matcher.Embedder
	logger   *log.Logger
}

// NewTag wires the store; embedder is the matcher-side vocabulary the
// tag mutations keep in sync.
func NewTag(client *database.Client, logger *log.Logger, embedder matcher.Embedder) *Tag {
	return &Tag{
		client:   client,
		embedder: embedder,
		logger:   logger,
	}
}

// Close releases the embedder.
func (s *Tag) Close() error {
	s.embedder.Close()
	return nil
}

// Get returns one tag by id, errs.KindNotFound when absent.
func (s *Tag) Get(ctx context.Context, id int64) (database.Tag, error) {
	tag, err := s.client.Queries.GetTag(ctx, id)
	if err != nil {
		return database.Tag{}, errs.FromDB(err, "get tag")
	}
	return tag, nil
}

// GetByName returns one tag by its exact name.
func (s *Tag) GetByName(ctx context.Context, name string) (database.Tag, error) {
	tag, err := s.client.Queries.GetTagByName(ctx, name)
	if err != nil {
		return database.Tag{}, errs.FromDB(err, "get tag by name")
	}
	return tag, nil
}

// Count returns how many tags exist.
func (s *Tag) Count(ctx context.Context) (int64, error) {
	count, err := s.client.Queries.CountTags(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count tags")
	}
	return count, nil
}

// CountByName counts the tags whose names start with the prefix.
func (s *Tag) CountByName(ctx context.Context, prefix string) (int64, error) {
	count, err := s.client.Queries.CountTagsByName(ctx, prefix+"%")
	if err != nil {
		return 0, errs.FromDB(err, "count tags by name")
	}
	return count, nil
}

// ListAll returns every tag, promoted and provisional.
func (s *Tag) ListAll(ctx context.Context) ([]database.Tag, error) {
	tags, err := s.client.Queries.ListAllTags(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "list all tags")
	}
	return tags, nil
}

// ListWithDocumentCount pages all tags with their live document counts.
func (s *Tag) ListWithDocumentCount(ctx context.Context, limit, offset int32) ([]database.ListTagsWithDocumentCountRow, error) {
	tags, err := s.client.Queries.ListTagsWithDocumentCount(ctx, database.ListTagsWithDocumentCountParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list tags with document count")
	}
	return tags, nil
}

// SearchByNameWithDocumentCount pages the tags whose names start with the
// prefix, with their live document counts.
func (s *Tag) SearchByNameWithDocumentCount(ctx context.Context, prefix string, limit, offset int32) ([]database.SearchTagsByNameWithDocumentCountRow, error) {
	tags, err := s.client.Queries.SearchTagsByNameWithDocumentCount(ctx, database.SearchTagsByNameWithDocumentCountParams{
		Name:   prefix + "%",
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "search tags with document count")
	}
	return tags, nil
}

// ListProvisional pages the not-yet-promoted tags with document counts.
func (s *Tag) ListProvisional(ctx context.Context, limit, offset int32) ([]database.ListProvisionalTagsWithDocumentCountRow, error) {
	tags, err := s.client.Queries.ListProvisionalTagsWithDocumentCount(ctx, database.ListProvisionalTagsWithDocumentCountParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, errs.FromDB(err, "list provisional tags")
	}
	return tags, nil
}

// CountProvisional counts the not-yet-promoted tags.
func (s *Tag) CountProvisional(ctx context.Context) (int64, error) {
	count, err := s.client.Queries.CountProvisionalTags(ctx)
	if err != nil {
		return 0, errs.FromDB(err, "count provisional tags")
	}
	return count, nil
}

// LatestQualityEventsForNames returns the most recent quality verdict per
// tag name under the current quality rule version.
func (s *Tag) LatestQualityEventsForNames(ctx context.Context, names []string) ([]database.LatestTagQualityEventsForNamesRow, error) {
	rows, err := s.client.Queries.LatestTagQualityEventsForNames(ctx, database.LatestTagQualityEventsForNamesParams{
		PolicyVersion: analyzer.TagQualityVersion,
		TagNames:      names,
	})
	if err != nil {
		return nil, errs.FromDB(err, "latest tag quality events")
	}
	return rows, nil
}

// PendingIdentityCounts returns, per name, how many identity events are
// still pending judgement.
func (s *Tag) PendingIdentityCounts(ctx context.Context, names []string) (map[string]int64, error) {
	rows, err := s.client.Queries.PendingTagIdentityCountsByNames(ctx, names)
	if err != nil {
		return nil, errs.FromDB(err, "pending tag identity counts")
	}
	counts := make(map[string]int64, len(rows))
	for _, row := range rows {
		counts[row.Name] = row.PendingCount
	}
	return counts, nil
}

// Create plans and applies registrations for the names in one
// transaction: conflicts resolve to the existing tag, aliases apply to
// their canonical, and new tags are born (provisional where the identity
// machinery flags them) and embedded into the matcher store.
func (s *Tag) Create(ctx context.Context, names []string) ([]service.CreateResult[database.Tag], error) {
	return s.create(ctx, names, false)
}

// CreateAndUnblock is Create plus clearing the deleted-tag blocklist
// entries of everything it created, resolved or aliased, so the
// cleared-blocklist path keeps working. The blocklist re-check inside the
// plan is exempted for the same reason.
func (s *Tag) CreateAndUnblock(ctx context.Context, names []string) ([]service.CreateResult[database.Tag], error) {
	results, err := s.create(ctx, names, true)
	if err != nil {
		return nil, err
	}
	for _, r := range results {
		switch r.Status {
		case service.Created, service.Conflict, service.Apply, service.Alias:
		default:
			continue
		}
		if err := s.client.Queries.DeleteDeletedTag(ctx, r.Entity.Name); err != nil {
			return nil, errs.FromDB(err, "clear deleted tag "+r.Entity.Name)
		}
	}
	return results, nil
}

func (s *Tag) create(ctx context.Context, names []string, ignoreBlocklist bool) ([]service.CreateResult[database.Tag], error) {
	plan, err := s.buildPlan(ctx, s.client.Queries, names, ignoreBlocklist)
	if err != nil {
		return nil, err
	}

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return nil, errs.FromDB(err, "begin tag create")
	}
	defer tx.Rollback()

	txQ := s.client.Queries.WithTx(tx)
	results, newNames, err := s.CreatePlannedTx(ctx, txQ, plan, false)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, errs.FromDB(err, "commit tag create")
	}

	if len(newNames) > 0 {
		s.encodeAndAddBatch(ctx, newNames)
	}
	return results, nil
}

// PlanRegistration plans the registrations for the names without running
// any judging call in the request: identity evidence is recorded and
// judgement happens out of band.
func (s *Tag) PlanRegistration(ctx context.Context, names []string) (tags.Plan, error) {
	return s.buildPlan(ctx, s.client.Queries, names, false)
}

func (s *Tag) buildPlan(ctx context.Context, q *database.Queries, names []string, ignoreBlocklist bool) (tags.Plan, error) {
	view, err := s.storeView(ctx, q, names, ignoreBlocklist)
	if err != nil {
		return tags.Plan{}, err
	}
	plan := tags.PlanDeterministic(names, view)
	plan.SkipBlocklistCheck = ignoreBlocklist
	return plan, nil
}

func (s *Tag) storeView(ctx context.Context, q *database.Queries, names []string, ignoreBlocklist bool) (tags.StoreView, error) {
	allTags, err := q.ListAllTags(ctx)
	if err != nil {
		return tags.StoreView{}, errs.FromDB(err, "load existing tags")
	}
	view := tags.StoreView{
		Names:   make([]string, 0, len(allTags)),
		Aliases: map[string]string{},
		Blocked: map[string]struct{}{},
	}
	for _, t := range allTags {
		view.Names = append(view.Names, t.Name)
	}

	aliases, err := tags.ResolveAliases(ctx, q, names)
	if err != nil {
		return tags.StoreView{}, errs.FromDB(err, "load tag aliases")
	}
	view.Aliases = aliases

	if !ignoreBlocklist {
		deleted, err := q.ListAllDeletedTags(ctx)
		if err != nil {
			return tags.StoreView{}, errs.FromDB(err, "load deleted tags")
		}
		for _, row := range deleted {
			view.Blocked[row.Name] = struct{}{}
		}
	}
	return view, nil
}

// CreatePlannedTx applies a planned registration set inside the caller's
// transaction: it resolves each disposition against the live store, writes
// the plan's aliases, records the plan's identity evidence, and promotes
// non-provisional new tags unless quarantined. It returns the per-
// registration results and the names it created.
func (s *Tag) CreatePlannedTx(ctx context.Context, q *database.Queries, plan tags.Plan, quarantined bool) ([]service.CreateResult[database.Tag], []string, error) {
	results := make([]service.CreateResult[database.Tag], len(plan.Registrations))
	var newNames []string
	var promoteIDs []int64

	existingTags, err := q.ListAllTags(ctx)
	if err != nil {
		return nil, nil, errs.FromDB(err, "load existing tags")
	}
	existingMap := make(map[string]database.Tag, len(existingTags))
	byKey := make(map[string]database.Tag, len(existingTags))
	for _, t := range existingTags {
		existingMap[t.Name] = t
		if _, ok := byKey[tags.Key(t.Name)]; !ok {
			byKey[tags.Key(t.Name)] = t
		}
	}

	aliasNames := make([]string, 0, len(plan.Registrations)+len(plan.Aliases))
	for _, reg := range plan.Registrations {
		if name := textnorm.NormalizeTag(reg.Name); name != "" {
			aliasNames = append(aliasNames, name)
		}
	}
	for _, action := range plan.Aliases {
		if name := textnorm.NormalizeTag(action.AliasName); name != "" {
			aliasNames = append(aliasNames, name)
		}
	}
	aliases, err := tags.ResolveAliases(ctx, q, aliasNames)
	if err != nil {
		return nil, nil, errs.FromDB(err, "load tag aliases")
	}

	blocked := map[string]struct{}{}
	if !plan.SkipBlocklistCheck {
		deleted, err := q.ListAllDeletedTags(ctx)
		if err != nil {
			return nil, nil, errs.FromDB(err, "load deleted tags")
		}
		for _, row := range deleted {
			blocked[row.Name] = struct{}{}
		}
	}

	for i, reg := range plan.Registrations {
		name := textnorm.NormalizeTag(reg.Name)

		if reg.Disposition == tags.DispositionInvalid {
			results[i] = service.CreateResult[database.Tag]{Status: service.Invalid}
			continue
		}
		if reg.Disposition == tags.DispositionAlias {
			canonical, ok := existingMap[textnorm.NormalizeTag(reg.Resolved)]
			if !ok {
				results[i] = service.CreateResult[database.Tag]{Status: service.Invalid}
				continue
			}
			results[i] = service.CreateResult[database.Tag]{Entity: canonical, Status: service.Alias}
			continue
		}
		if name == "" {
			results[i] = service.CreateResult[database.Tag]{Status: service.Invalid}
			continue
		}
		if _, isBlocked := blocked[name]; isBlocked {
			results[i] = service.CreateResult[database.Tag]{Status: service.Invalid}
			continue
		}

		if canonicalName, ok := aliases[name]; ok {
			canonical, ok := existingMap[canonicalName]
			if !ok {
				results[i] = service.CreateResult[database.Tag]{Status: service.Invalid}
				continue
			}
			results[i] = service.CreateResult[database.Tag]{Entity: canonical, Status: service.Apply}
			continue
		}

		if existing, ok := existingMap[name]; ok {
			results[i] = service.CreateResult[database.Tag]{Entity: existing, Status: service.Conflict}
			continue
		}

		if reg.Resolved != "" {
			if canonical, ok := existingMap[textnorm.NormalizeTag(reg.Resolved)]; ok {
				status := service.Apply
				if reg.Disposition == tags.DispositionConflict {
					status = service.Conflict
				}
				results[i] = service.CreateResult[database.Tag]{Entity: canonical, Status: status}
				continue
			}
		}

		if existing, ok := byKey[tags.Key(name)]; ok && existing.Name != name {
			results[i] = service.CreateResult[database.Tag]{Entity: existing, Status: service.Apply}
			continue
		}

		id, err := q.CreateTag(ctx, name)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				existing, err := q.GetTagByName(ctx, name)
				if err != nil {
					return nil, nil, errs.FromDB(err, "get tag by name after conflict "+name)
				}
				results[i] = service.CreateResult[database.Tag]{Entity: existing, Status: service.Conflict}
				continue
			}
			return nil, nil, errs.FromDB(err, "create tag "+name)
		}

		created := database.Tag{ID: id, Name: name}
		existingMap[name] = created
		if _, ok := byKey[tags.Key(name)]; !ok {
			byKey[tags.Key(name)] = created
		}
		results[i] = service.CreateResult[database.Tag]{Entity: created, Status: service.Created}
		newNames = append(newNames, name)
		if !reg.Provisional {
			promoteIDs = append(promoteIDs, id)
		}
	}

	for _, action := range plan.Aliases {
		if _, isBlocked := blocked[textnorm.NormalizeTag(action.AliasName)]; isBlocked {
			continue
		}
		if err := s.applyAliasAction(ctx, q, existingMap, action); err != nil {
			return nil, nil, err
		}
	}

	if err := s.recordIdentityEvidence(ctx, q, plan.Evidence); err != nil {
		return nil, nil, err
	}

	if !quarantined && len(promoteIDs) > 0 {
		if _, err := q.PromoteTags(ctx, promoteIDs); err != nil {
			return nil, nil, errs.FromDB(err, "promote new tags")
		}
	}

	return results, newNames, nil
}

// ApplyAliasPlan applies the planned alias actions in one transaction:
// blocked names are skipped, the loser's own aliases are retargeted (no
// alias chains), its documents are reassigned and its tag row deleted
// when RemoveExistingTag is set, and the removed names leave the matcher
// store after commit.
func (s *Tag) ApplyAliasPlan(ctx context.Context, actions []tags.AliasAction) error {
	if len(actions) == 0 {
		return nil
	}
	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return errs.FromDB(err, "begin alias plan")
	}
	defer tx.Rollback()
	txQ := s.client.Queries.WithTx(tx)

	existingTags, err := txQ.ListAllTags(ctx)
	if err != nil {
		return errs.FromDB(err, "load existing tags")
	}
	existing := make(map[string]database.Tag, len(existingTags))
	for _, t := range existingTags {
		existing[t.Name] = t
	}

	deleted, err := txQ.ListAllDeletedTags(ctx)
	if err != nil {
		return errs.FromDB(err, "load deleted tags")
	}
	blocked := make(map[string]struct{}, len(deleted))
	for _, row := range deleted {
		blocked[row.Name] = struct{}{}
	}

	var removed []string
	for _, action := range actions {
		if _, isBlocked := blocked[textnorm.NormalizeTag(action.AliasName)]; isBlocked {
			continue
		}
		if err := s.applyAliasAction(ctx, txQ, existing, action); err != nil {
			return err
		}
		if action.RemoveExistingTag {
			removed = append(removed, action.AliasName)
		}
	}

	if err := tx.Commit(); err != nil {
		return errs.FromDB(err, "commit alias plan")
	}
	if len(removed) > 0 {
		if err := s.embedder.RemoveFromStore(ctx, removed); err != nil {
			s.logger.Error(nil, "tag service: remove aliased names from store: %v", err)
		}
	}
	return nil
}

func (s *Tag) applyAliasAction(ctx context.Context, q *database.Queries, existing map[string]database.Tag, action tags.AliasAction) error {
	aliasName := textnorm.NormalizeTag(action.AliasName)
	canonicalName := textnorm.NormalizeTag(action.Canonical)
	if aliasName == "" || canonicalName == "" || aliasName == canonicalName {
		return nil
	}

	canonical, ok := existing[canonicalName]
	if !ok {
		tag, err := q.GetTagByName(ctx, canonicalName)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return errs.FromDB(err, "get alias target "+canonicalName)
		}
		canonical = tag
	}

	if _, err := q.GetTagAliasByName(ctx, aliasName); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return errs.FromDB(err, "get tag alias "+aliasName)
	}

	if action.RemoveExistingTag {
		loser, err := q.GetTagForUpdateByName(ctx, aliasName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return errs.FromDB(err, "get alias loser "+aliasName)
		}
		if loser.ID == canonical.ID {
			return nil
		}

		reassigned, err := q.ReassignDocumentTags(ctx, database.ReassignDocumentTagsParams{
			SourceID: loser.ID,
			TargetID: canonical.ID,
		})
		if err != nil {
			return errs.FromDB(err, "reassign document tags for alias "+aliasName)
		}
		if reassigned > 0 {
			if err := q.ClearOrphanTagMarksByTagIDs(ctx, []int64{canonical.ID}); err != nil {
				return errs.FromDB(err, "clear orphan tag marks for alias "+aliasName)
			}
		}
		if _, err := q.DeleteDocumentTagsForTag(ctx, loser.ID); err != nil {
			return errs.FromDB(err, "delete leftover document tags for alias "+aliasName)
		}
		// no alias chains: the loser's own aliases re-target the new canonical
		if _, err := q.RetargetTagAliases(ctx, database.RetargetTagAliasesParams{
			NewTagID: canonical.ID,
			OldTagID: loser.ID,
		}); err != nil {
			return errs.FromDB(err, "retarget aliases for "+aliasName)
		}
		if err := q.DeleteTag(ctx, loser.ID); err != nil {
			return errs.FromDB(err, "delete alias loser "+aliasName)
		}
	}

	if _, err := q.InsertTagAlias(ctx, database.InsertTagAliasParams{
		AliasName:      aliasName,
		CanonicalTagID: canonical.ID,
		RuleSetVersion: tags.IdentityRuleSetVersion,
	}); err != nil {
		return errs.FromDB(err, "insert tag alias "+aliasName)
	}
	return nil
}

func (s *Tag) recordIdentityEvidence(ctx context.Context, q *database.Queries, rows []tags.EvidenceRow) error {
	for _, row := range rows {
		candidate := textnorm.NormalizeTag(row.Candidate)
		if candidate == "" {
			continue
		}
		criteria := row.Criteria
		if len(criteria) == 0 {
			criteria = json.RawMessage("{}")
		}
		var partner sql.NullString
		if p := textnorm.NormalizeTag(row.Partner); p != "" {
			partner = sql.NullString{String: p, Valid: true}
		}
		var verdict, model sql.NullString
		if row.Verdict != "" {
			verdict = sql.NullString{String: row.Verdict, Valid: true}
		}
		if row.Model != "" {
			model = sql.NullString{String: row.Model, Valid: true}
		}
		if _, err := q.InsertTagIdentityEvent(ctx, database.InsertTagIdentityEventParams{
			Kind:           row.Kind,
			Candidate:      candidate,
			Partner:        partner,
			Criteria:       criteria,
			Verdict:        verdict,
			Model:          model,
			RuleSetVersion: tags.IdentityRuleSetVersion,
		}); err != nil {
			return errs.FromDB(err, "insert tag identity event "+candidate)
		}
	}
	return nil
}

// AddToStore embeds the names into the matcher store, best-effort.
func (s *Tag) AddToStore(ctx context.Context, names []string) {
	if len(names) == 0 {
		return
	}
	s.encodeAndAddBatch(ctx, names)
}

// Update renames tags in bulk, returning one result per pair (invalid,
// not found, no-op, conflict, or updated). Renamed promoted tags are
// re-embedded and their old names leave the matcher store.
func (s *Tag) Update(ctx context.Context, pairs []TagUpdatePair) ([]service.UpdateResult[database.Tag], error) {
	results := make([]service.UpdateResult[database.Tag], len(pairs))
	var newNames []string
	type rename struct {
		idx     int
		oldName string
		name    string
	}
	var renames []rename

	allTags, err := s.client.Queries.ListAllTags(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load all tags")
	}
	tagMap := make(map[int64]database.Tag, len(allTags))
	nameMap := make(map[string]database.Tag, len(allTags))
	for _, t := range allTags {
		tagMap[t.ID] = t
		nameMap[t.Name] = t
	}

	for i, p := range pairs {
		name := textnorm.NormalizeTag(p.Name)
		if name == "" {
			results[i] = service.UpdateResult[database.Tag]{Status: service.UpdateInvalid}
			continue
		}

		old, ok := tagMap[p.ID]
		if !ok {
			results[i] = service.UpdateResult[database.Tag]{Status: service.UpdateNotFound}
			continue
		}

		if name == old.Name {
			results[i] = service.UpdateResult[database.Tag]{Entity: old, Status: service.Noop}
			continue
		}

		if existing, ok := nameMap[name]; ok && existing.ID != p.ID {
			results[i] = service.UpdateResult[database.Tag]{Entity: existing, Status: service.UpdateConflict}
			continue
		}

		if err := s.client.Queries.UpdateTag(ctx, database.UpdateTagParams{
			Name: textnorm.NormalizeTag(name),
			ID:   p.ID,
		}); err != nil {
			if errs.IsConstraint(err) {
				var existing database.Tag
				if e, ok := nameMap[name]; ok {
					existing = e
				}
				results[i] = service.UpdateResult[database.Tag]{Entity: existing, Status: service.UpdateConflict}
				continue
			}
			return nil, errs.FromDB(err, "update tag")
		}

		if old.PromotedAt.Valid {
			newNames = append(newNames, name)
		}
		renames = append(renames, rename{idx: i, oldName: old.Name, name: name})
	}

	for _, r := range renames {
		if err := s.embedder.RemoveFromStore(ctx, []string{r.oldName}); err != nil {
			s.logger.Error(nil, "tag service: remove from store: %v", err)
		}
	}

	if len(newNames) > 0 {
		s.encodeAndAddBatch(ctx, newNames)
	}

	for _, r := range renames {
		updatedTag := database.Tag{
			ID:   pairs[r.idx].ID,
			Name: r.name,
		}
		results[r.idx] = service.UpdateResult[database.Tag]{Entity: updatedTag, Status: service.Updated}
	}

	return results, nil
}

// Delete removes tags and records each name in the deleted-tag blocklist
// (kind blocked) so the same vocabulary is not recreated silently; their
// names leave the matcher store.
func (s *Tag) Delete(ctx context.Context, ids []int64) ([]service.DeleteResult, error) {
	results := make([]service.DeleteResult, len(ids))

	allTags, err := s.client.Queries.ListAllTags(ctx)
	if err != nil {
		return nil, errs.FromDB(err, "load all tags")
	}
	tagMap := make(map[int64]database.Tag, len(allTags))
	for _, t := range allTags {
		tagMap[t.ID] = t
	}

	for i, id := range ids {
		tag, ok := tagMap[id]
		if !ok {
			results[i] = service.DeleteResult{ID: id, Status: service.DeleteNotFound}
			continue
		}

		if err := s.client.Queries.DeleteTag(ctx, id); err != nil {
			return nil, errs.FromDB(err, "delete tag")
		}

		if err := s.client.Queries.UpsertDeletedTag(ctx, database.UpsertDeletedTagParams{
			Name: textnorm.NormalizeTag(tag.Name),
			Kind: DeletedTagKindBlocked,
		}); err != nil {
			s.logger.Error(nil, "tag service: record deleted tag %q: %v", tag.Name, err)
		}

		if err := s.embedder.RemoveFromStore(ctx, []string{tag.Name}); err != nil {
			s.logger.Error(nil, "tag service: remove from store: %v", err)
		}
		results[i] = service.DeleteResult{ID: id, Status: service.Deleted}
	}

	return results, nil
}

// Promote promotes one provisional tag and embeds it; already-promoted
// and not-found cases are reported through the status, and reason goes to
// the audit log.
func (s *Tag) Promote(ctx context.Context, id int64, reason string) (database.Tag, service.PromoteStatus, error) {
	tag, err := s.Get(ctx, id)
	if err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			return database.Tag{}, service.PromoteNotFound, nil
		}
		return database.Tag{}, service.PromoteNotFound, err
	}
	if tag.PromotedAt.Valid {
		return tag, service.PromoteAlreadyPromoted, nil
	}

	promoted, err := s.client.Queries.PromoteTags(ctx, []int64{id})
	if err != nil {
		return tag, service.PromoteNotFound, errs.FromDB(err, "promote tag")
	}
	if promoted == 0 {
		return tag, service.PromoteAlreadyPromoted, nil
	}

	s.encodeAndAddBatch(ctx, []string{tag.Name})
	s.logger.Info(nil, "tagquality[%s]: promote %q (reason=%s)", analyzer.TagQualityVersion, tag.Name, reason)
	return tag, service.PromotePromoted, nil
}

// ListDeleted pages the deleted-tag blocklist by name prefix with its
// total count.
func (s *Tag) ListDeleted(ctx context.Context, prefix string, limit, offset int32) ([]database.DeletedTag, int64, error) {
	rows, err := s.client.Queries.ListDeletedTags(ctx, database.ListDeletedTagsParams{
		NamePrefix: prefix,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, 0, errs.FromDB(err, "list deleted tags")
	}
	total, err := s.client.Queries.CountDeletedTags(ctx, prefix)
	if err != nil {
		return nil, 0, errs.FromDB(err, "count deleted tags")
	}
	return rows, total, nil
}

// RestoreDeleted drops a deleted-tag record; blocked entries have their
// tag recreated through Create. Merged entries only clear the blocklist.
func (s *Tag) RestoreDeleted(ctx context.Context, name string) (database.DeletedTag, error) {
	row, err := s.client.Queries.GetDeletedTag(ctx, name)
	if err != nil {
		return database.DeletedTag{}, errs.FromDB(err, "restore deleted tag")
	}
	if row.Kind == DeletedTagKindBlocked {
		if _, err := s.Create(ctx, []string{row.Name}); err != nil {
			return row, err
		}
	}
	if err := s.client.Queries.DeleteDeletedTag(ctx, name); err != nil {
		return row, errs.FromDB(err, "restore deleted tag")
	}
	return row, nil
}

// RemoveFromStore drops the names from the matcher store's embeddings.
func (s *Tag) RemoveFromStore(ctx context.Context, names []string) error {
	return s.embedder.RemoveFromStore(ctx, names)
}

func (s *Tag) encodeAndAddBatch(ctx context.Context, names []string) {
	if err := s.embedder.AddToStore(ctx, names); err != nil {
		s.logger.Error(nil, "tag service: add to store: %v", err)
	}
}
