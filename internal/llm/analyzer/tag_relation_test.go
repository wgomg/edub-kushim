package analyzer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestParseTagRelationResponse_RoundTrip pins the gate's happy path: both answers land in their slots, and
// the prompt carries each pair's term, anchor, and proposal source, with the similarity only where it exists.
func TestParseTagRelationResponse_RoundTrip(t *testing.T) {
	reply := `{"pairs":[
		{"index":0,"related":true},
		{"index":1,"related":false}
	]}`
	results, err := ParseTagRelationResponse(reply, 2)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !reflect.DeepEqual(results, []TagRelationResult{
		{Index: 0, Related: true},
		{Index: 1, Related: false},
	}) {
		t.Errorf("results = %+v, want the two verdicts in their slots", results)
	}

	similarity := 0.83
	prompt := BuildTagRelationPrompt([]TagRelationCandidate{
		{Index: 0, Term: "mexican", Anchor: "mexico", Source: "morphographic", Similarity: &similarity},
		{Index: 1, Term: "spudgers", Anchor: "spudger", Source: "plural strip"},
	})
	for _, want := range []string{`"mexican"`, `"mexico"`, `"morphographic"`, "estimated_similarity=0.83", `"spudgers"`, `"spudger"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	if got := strings.Count(prompt, "estimated_similarity"); got != 1 {
		t.Errorf("estimated_similarity appears %d times, want only on the pair whose anchor ranked", got)
	}
	if !strings.Contains(TagRelationSystemMessage, "weak evidence, not as a verdict") {
		t.Error("the system message must keep a similarity estimate weak, never a verdict")
	}
}

// TestParseTagRelationResponse_MissingEntryIsNeverUnrelated pins the driver's safety property: a pair the
// judge skipped must surface as *IncompleteReplyError, so an unanswered candidate can never close its
// proposal as unrelated and silently drop a fold.
func TestParseTagRelationResponse_MissingEntryIsNeverUnrelated(t *testing.T) {
	truncated := `{"pairs":[{"index":0,"related":true}]}`
	results, err := ParseTagRelationResponse(truncated, 2)
	incomplete, ok := errors.AsType[*IncompleteReplyError](err)
	if !ok {
		t.Fatalf("error type = %T, want *IncompleteReplyError carrying the missing indices", err)
	}
	if len(incomplete.Missing) != 1 || incomplete.Missing[0] != 1 {
		t.Errorf("Missing = %v, want [1]", incomplete.Missing)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 so the answered entry stays readable", len(results))
	}
	if !results[0].Related {
		t.Errorf("index 0 = %+v, want the answered verdict salvaged", results[0])
	}
}

// TestParseTagRelationResponse_OmittedRelatedIsMissing pins the pointer discipline: an entry that names its
// index but leaves related absent or null counts as unanswered, never as an unrelated verdict.
func TestParseTagRelationResponse_OmittedRelatedIsMissing(t *testing.T) {
	for _, reply := range []string{
		`{"pairs":[{"index":0}]}`,
		`{"pairs":[{"index":0,"related":null}]}`,
	} {
		_, err := ParseTagRelationResponse(reply, 1)
		incomplete, ok := errors.AsType[*IncompleteReplyError](err)
		if !ok {
			t.Fatalf("reply %s: error type = %T, want *IncompleteReplyError", reply, err)
		}
		if len(incomplete.Missing) != 1 || incomplete.Missing[0] != 0 {
			t.Errorf("reply %s: Missing = %v, want [0]", reply, incomplete.Missing)
		}
	}
}

func TestParseTagRelationResponse_BareArrayPositional(t *testing.T) {
	bare := `[{"related":true},{"related":false}]`
	results, err := ParseTagRelationResponse(bare, 2)
	if err != nil {
		t.Fatalf("a bare array envelope must parse: %v", err)
	}
	if !reflect.DeepEqual(results, []TagRelationResult{
		{Index: 0, Related: true},
		{Index: 1, Related: false},
	}) {
		t.Errorf("results = %+v, want the entries placed by position", results)
	}
}

func TestParseTagRelationResponse_OutOfRangeIndexIsIgnored(t *testing.T) {
	reply := `{"pairs":[{"index":7,"related":true}]}`
	_, err := ParseTagRelationResponse(reply, 1)
	incomplete, ok := errors.AsType[*IncompleteReplyError](err)
	if !ok {
		t.Fatalf("error type = %T, want *IncompleteReplyError: the reply answered no real candidate", err)
	}
	if len(incomplete.Missing) != 1 || incomplete.Missing[0] != 0 {
		t.Errorf("Missing = %v, want [0]", incomplete.Missing)
	}
}

// TestParseTagRelationResponse_ExplicitIndexWinsOverPosition pins the placement a truncated reply depends
// on: a gap shifts every entry away from its position, so the entry's own index is what lands it in the
// slot the driver keys by candidate.
func TestParseTagRelationResponse_ExplicitIndexWinsOverPosition(t *testing.T) {
	shifted := `{"pairs":[
		{"index":1,"related":false},
		{"index":0,"related":true}
	]}`
	results, err := ParseTagRelationResponse(shifted, 3)
	incomplete, ok := errors.AsType[*IncompleteReplyError](err)
	if !ok || len(incomplete.Missing) != 1 || incomplete.Missing[0] != 2 {
		t.Fatalf("error = %v, want a single missing index 2", err)
	}
	if !results[0].Related || results[1].Related {
		t.Errorf("results = %+v, want index 0 salvaged as related and index 1 as unrelated", results)
	}
}
