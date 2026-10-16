package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TagAuditDedupKey is the active-dedup key shared by the enqueue path and the task handler so they agree.
const TagAuditDedupKey = "tagaudit"

// TagAudit enqueues completeness audits and serves the coverage review surface: the flags list and the
// apply, dismiss, and create-value actions.
type TagAudit struct {
	queries      *database.Queries
	taskCreator  types.TaskCreator
	batchCreator types.BatchCreator
}

// NewTagAudit wires the audit enqueue path and review surface to the queries and the batch and task
// creators.
func NewTagAudit(queries *database.Queries, taskCreator types.TaskCreator, batchCreator types.BatchCreator) *TagAudit {
	return &TagAudit{queries: queries, taskCreator: taskCreator, batchCreator: batchCreator}
}

// CoverageFlag is one open coverage review item, as the review surface lists it.
type CoverageFlag struct {
	ID            int64
	DocumentID    int64
	DocumentTitle string
	TagName       string
	Class         string
	Source        string
	Provisional   bool
	CreatedAt     time.Time
}

// Enqueue queues a completeness audit batch and its task, or reports alreadyQueued when one is still
// active.
func (s *TagAudit) Enqueue(ctx context.Context) (batchID string, alreadyQueued bool, err error) {
	active, err := s.queries.CountActiveTagAuditBatches(ctx)
	if err != nil {
		return "", false, errs.FromDB(err, "count active tag audit batches")
	}
	if active > 0 {
		return "", true, nil
	}

	batchID = uuid.New().String()
	taskID := uuid.New().String()

	// a unique violation on the active-dedup index means another audit task already owns the slot (the count
	// above lost a race); anything else is a real failure and must not be reported as already queued
	if _, err := s.taskCreator.CreateTask(ctx, types.Task.Type.TagAudit, batchID, json.RawMessage("{}"), taskID, types.Task.Status.Pending, TagAuditDedupKey); err != nil {
		if errs.IsConstraint(err) {
			return "", true, nil
		}
		return "", false, errs.FromDB(err, "create tag audit task")
	}

	if err := s.batchCreator.Create(ctx, batchID, types.Batch.Source.TagAudit, types.Batch.Status.Queued); err != nil {
		return "", false, errs.FromDB(errors.Join(err, task.DeleteBatchTasks(context.Background(), s.queries, batchID)), "create tag audit batch")
	}

	return batchID, false, nil
}

// ListFlags returns the open coverage flags, newest first, with the total count.
func (s *TagAudit) ListFlags(ctx context.Context, limit, offset int32) ([]CoverageFlag, int64, error) {
	rows, err := s.queries.ListOpenTagCoverageFlags(ctx, database.ListOpenTagCoverageFlagsParams{PageLimit: limit, PageOffset: offset})
	if err != nil {
		return nil, 0, errs.FromDB(err, "list open coverage flags")
	}
	total, err := s.queries.CountOpenTagCoverageFlags(ctx)
	if err != nil {
		return nil, 0, errs.FromDB(err, "count open coverage flags")
	}
	flags := make([]CoverageFlag, len(rows))
	for i, r := range rows {
		flags[i] = CoverageFlag{
			ID:            r.ID,
			DocumentID:    r.DocumentID,
			DocumentTitle: r.DocumentTitle,
			TagName:       r.TagName,
			Class:         r.Class,
			Source:        r.Source,
			Provisional:   r.Provisional,
			CreatedAt:     r.CreatedAt,
		}
	}
	return flags, total, nil
}

// ApplyFlag attaches the flagged dimension to the document and records the applied review outcome: the
// operator confirms the gap the audit found.
func (s *TagAudit) ApplyFlag(ctx context.Context, documentID int64, tagName string) error {
	tag, err := s.queries.GetTagByName(ctx, tagName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errs.ENotFound("apply coverage flag", err)
		}
		return errs.FromDB(err, "load flagged tag")
	}
	pair, err := s.queries.LatestTagCoverageEventForPair(ctx, database.LatestTagCoverageEventForPairParams{DocumentID: documentID, TagName: tagName})
	if err != nil {
		return errs.FromDB(err, "load coverage flag")
	}
	if err := s.queries.AddDocumentTag(ctx, database.AddDocumentTagParams{
		DocumentID: documentID,
		TagID:      tag.ID,
		Source:     DocumentTagSourceAudit,
	}); err != nil {
		return errs.FromDB(err, "attach audited tag")
	}
	return s.queries.InsertTagCoverageAction(ctx, database.InsertTagCoverageActionParams{
		DocumentID:      documentID,
		TagName:         tagName,
		Class:           string(tags.CoverageExplicit),
		Source:          pair.Source,
		Verdict:         json.RawMessage("{}"),
		TextHash:        pair.TextHash,
		CriteriaVersion: pair.CriteriaVersion,
		Action:          string(tags.CoverageActionFill),
		ReviewOutcome:   sql.NullString{String: "applied", Valid: true},
		RuleSetVersion:  tags.RuleSetVersion,
	})
}

// DismissFlag records the operator's not-applies outcome: the dismissed pair is authoritative and the next
// audit run does not re-flag it while the document text is unchanged.
func (s *TagAudit) DismissFlag(ctx context.Context, documentID int64, tagName string) error {
	pair, err := s.queries.LatestTagCoverageEventForPair(ctx, database.LatestTagCoverageEventForPairParams{DocumentID: documentID, TagName: tagName})
	if err != nil {
		return errs.FromDB(err, "load coverage flag")
	}
	return s.queries.InsertTagCoverageAction(ctx, database.InsertTagCoverageActionParams{
		DocumentID:      documentID,
		TagName:         tagName,
		Class:           string(tags.CoverageNotApplies),
		Source:          pair.Source,
		Verdict:         json.RawMessage("{}"),
		TextHash:        pair.TextHash,
		CriteriaVersion: pair.CriteriaVersion,
		Action:          string(tags.CoverageActionNone),
		ReviewOutcome:   sql.NullString{String: "not_applies", Valid: true},
		RuleSetVersion:  tags.RuleSetVersion,
	})
}
