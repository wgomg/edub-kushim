package task

import (
	"encoding/json"
	"fmt"

	"github.com/wgomg/edub-kushim/internal/database"
)

// InheritPayloadKey names the successor inheritance contract inside a waiting task's payload: a JSON array
// of parent-result field names that the activation copies into the successor's payload before the successor
// becomes pending. The key has one owner, this package, so a producer declares which data a successor needs
// instead of writing activation code; a malformed list or a missing parent-result field discards the
// successor with the reason rather than activating it without the data it declared.
const InheritPayloadKey = "inherit"

// SuccessorPlan is one waiting successor's activation input, assembled by the runner from the successor's
// row, the parent's result and the successor's type definition. The store executes plans verbatim: a plan
// carrying Err is not activated but discarded with that error, so a successor whose payload cannot be
// completed never runs without the data it declared.
type SuccessorPlan struct {
	ID       int64
	TaskID   string
	Payload  json.RawMessage
	DedupKey string
	Err      error
}

// SuccessorOutcome reports what activating one planned successor did, for the runner's log. Err carries the
// plan's build error when the activation is DiscardedInvalid.
type SuccessorOutcome struct {
	TaskID     string
	Activation Activation
	Err        error
}

// planSuccessors builds one plan per waiting successor, merging the successor's declared inherit keys from
// the parent's result and deriving its dedup key through its type definition. A successor the plan cannot
// complete keeps its plan with Err set instead of failing the whole completion: the store discards that
// successor with the reason while its siblings still activate.
func planSuccessors(children []database.ListWaitingSuccessorsByParentRow, parentResult json.RawMessage, catalog TypeCatalog) []SuccessorPlan {
	// parse the result once: a valid non-object result simply has no fields, and the merge reports a declared
	// key as missing; malformed bytes are a distinct defect the merge reports as such
	var resultFields map[string]json.RawMessage
	var resultErr error
	switch {
	case len(parentResult) == 0:
	case !json.Valid(parentResult):
		resultErr = fmt.Errorf("parent result is not valid JSON")
	default:
		_ = json.Unmarshal(parentResult, &resultFields)
	}

	plans := make([]SuccessorPlan, 0, len(children))
	for _, child := range children {
		plans = append(plans, planSuccessor(child, resultFields, resultErr, catalog))
	}
	return plans
}

// planSuccessor assembles one successor's activation input, recording the first defect that makes the
// successor unusable in the plan's Err.
func planSuccessor(child database.ListWaitingSuccessorsByParentRow, resultFields map[string]json.RawMessage, resultErr error, catalog TypeCatalog) SuccessorPlan {
	plan := SuccessorPlan{ID: child.ID, TaskID: child.TaskID}

	payload, err := mergeInheritedFields(child, resultFields, resultErr)
	if err != nil {
		plan.Err = err
		return plan
	}
	plan.Payload = payload

	definition, err := catalog.Definition(child.TaskType)
	if err != nil {
		plan.Err = fmt.Errorf("definition for %s: %w", child.TaskType, err)
		return plan
	}
	if definition.DedupKey == nil {
		plan.Err = fmt.Errorf("task type %s declares no dedup key", child.TaskType)
		return plan
	}
	key := definition.DedupKey(payload)
	if key == "" {
		plan.Err = fmt.Errorf("task type %s derived an empty dedup key", child.TaskType)
		return plan
	}
	plan.DedupKey = key
	return plan
}

// mergeInheritedFields copies the parent-result fields a successor declares back into its stored payload. A
// successor that declares nothing keeps its payload byte for byte, so an unpolluted payload is never
// re-serialized, and a malformed parent result only matters to a successor that declares inheritance.
func mergeInheritedFields(child database.ListWaitingSuccessorsByParentRow, resultFields map[string]json.RawMessage, resultErr error) (json.RawMessage, error) {
	if child.Payload == nil {
		return nil, fmt.Errorf("waiting %s task %s has no payload", child.TaskType, child.TaskID)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(*child.Payload, &payload); err != nil {
		return nil, fmt.Errorf("parse payload of %s task %s: %w", child.TaskType, child.TaskID, err)
	}
	if payload == nil {
		return nil, fmt.Errorf("payload of %s task %s is not an object", child.TaskType, child.TaskID)
	}

	rawKeys, ok := payload[InheritPayloadKey]
	if !ok {
		return *child.Payload, nil
	}
	var keys []string
	if err := json.Unmarshal(rawKeys, &keys); err != nil {
		return nil, fmt.Errorf("parse %s of %s task %s: %w", InheritPayloadKey, child.TaskType, child.TaskID, err)
	}
	if len(keys) > 0 && resultErr != nil {
		return nil, resultErr
	}

	for _, key := range keys {
		value, ok := resultFields[key]
		if !ok {
			return nil, fmt.Errorf("parent result has no %q field for %s task %s", key, child.TaskType, child.TaskID)
		}
		payload[key] = value
	}

	merged, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload of %s task %s: %w", child.TaskType, child.TaskID, err)
	}
	return merged, nil
}
