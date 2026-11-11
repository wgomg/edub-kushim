package curation

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/errs"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/task"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TagRejudgeDedupKey derives the re-judge task's dedup key from its payload: one live re-judge per
// tag and scope, so a repeated click collapses while two different tags still queue.
func TagRejudgeDedupKey(payload json.RawMessage) string {
	var p struct {
		TagID int64  `json:"tag_id"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.TagID <= 0 || p.Scope == "" {
		return ""
	}
	return fmt.Sprintf("tagrejudge:%d:%s", p.TagID, p.Scope)
}

// TagRejudge enqueues one on-demand re-judge of a stored value: the quality judge over its first live
// document, or the composition judge over its compound, so the review queue can refresh a verdict
// without waiting for the next enrichment or reconciliation pass.
type TagRejudge struct {
	enqueuer *task.Enqueuer
}

// NewTagRejudge wires the enqueue path to the transactional enqueuer.
func NewTagRejudge(enqueuer *task.Enqueuer) *TagRejudge {
	return &TagRejudge{enqueuer: enqueuer}
}

// RejudgeSpecs returns the task set one re-judge enqueues.
func RejudgeSpecs(tagID int64, scope tags.RejudgeScope) ([]task.TaskSpec, error) {
	payload, err := json.Marshal(map[string]any{"tag_id": tagID, "scope": string(scope)})
	if err != nil {
		return nil, fmt.Errorf("marshal tag rejudge payload: %w", err)
	}
	return []task.TaskSpec{{Type: types.Task.Type.TagRejudge, Payload: payload}}, nil
}

// Enqueue queues one re-judge and reports alreadyQueued when the same tag and scope already has live
// work; the task dedup index owns that decision.
func (s *TagRejudge) Enqueue(ctx context.Context, spec task.BatchSpec, tagID int64, scope tags.RejudgeScope) (batchID string, alreadyQueued bool, err error) {
	specs, err := RejudgeSpecs(tagID, scope)
	if err != nil {
		return "", false, err
	}
	result, err := s.enqueuer.EnqueueBatch(ctx, spec, specs)
	if err != nil {
		return "", false, errs.FromDB(err, "create tag rejudge task")
	}
	if result.BatchID == "" {
		return "", true, nil
	}
	return result.BatchID, false, nil
}

// EnqueueMany queues a re-judge per tag in a single batch and reports alreadyQueued when every requested
// tag already has live work for the scope; the dedup index decides per task, so a partially covered batch
// still creates the missing ones.
func (s *TagRejudge) EnqueueMany(ctx context.Context, spec task.BatchSpec, tagIDs []int64, scope tags.RejudgeScope) (batchID string, alreadyQueued bool, err error) {
	specs := make([]task.TaskSpec, 0, len(tagIDs))
	for _, id := range tagIDs {
		tagSpecs, err := RejudgeSpecs(id, scope)
		if err != nil {
			return "", false, err
		}
		specs = append(specs, tagSpecs...)
	}
	result, err := s.enqueuer.EnqueueBatch(ctx, spec, specs)
	if err != nil {
		return "", false, errs.FromDB(err, "create tag rejudge tasks")
	}
	if result.BatchID == "" {
		return "", true, nil
	}
	return result.BatchID, false, nil
}
