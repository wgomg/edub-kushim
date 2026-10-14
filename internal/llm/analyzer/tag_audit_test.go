package analyzer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseTagAuditResponse_RoundTrip(t *testing.T) {
	candidates := []TagAuditCandidate{
		{Index: 0, Term: "russia"},
		{Index: 1, Term: "history"},
	}

	reply := `{"tags":[
		{"index":0,"applies":true,"defer":false},
		{"index":1,"applies":false,"defer":true}
	]}`
	results, err := ParseTagAuditResponse(reply, len(candidates))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if !results[0].Applies || results[0].Defer {
		t.Errorf("candidate 0 = %+v, want applies without defer", results[0])
	}
	if results[1].Applies || !results[1].Defer {
		t.Errorf("candidate 1 = %+v, want a not-applies abstention", results[1])
	}

	prompt := BuildTagAuditPrompt("doc text", candidates)
	if !strings.Contains(prompt, "doc text") {
		t.Error("prompt must carry the document text the about-ness test reads")
	}
	if !strings.Contains(prompt, "about-ness, not mention") {
		t.Error("prompt must state the applicability criterion")
	}
	for _, c := range candidates {
		if !strings.Contains(prompt, c.Term) {
			t.Errorf("prompt is missing candidate %q", c.Term)
		}
	}
}

// TestParseTagAuditResponse_SalvagesPartialReply pins the contract the driver depends on: a truncated reply
// hands back the entries it did answer alongside the missing indices, so a run judges the answered
// dimensions now and defers only the gaps to the next pass.
func TestParseTagAuditResponse_SalvagesPartialReply(t *testing.T) {
	partial := `{"tags":[
		{"index":0,"applies":true,"defer":false},
		{"index":2,"applies":true,"defer":true}
	]}`
	results, err := ParseTagAuditResponse(partial, 3)
	if err == nil {
		t.Fatal("a reply that skipped index 1 must report the gap")
	}
	incomplete, ok := errors.AsType[*IncompleteReplyError](err)
	if !ok {
		t.Fatalf("error type = %T, want *IncompleteReplyError carrying the missing indices", err)
	}
	if len(incomplete.Missing) != 1 || incomplete.Missing[0] != 1 {
		t.Errorf("Missing = %v, want [1]", incomplete.Missing)
	}
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3 so the answered entries stay readable", len(results))
	}
	if !results[0].Applies {
		t.Errorf("index 0 = %+v, want the answered verdict salvaged", results[0])
	}
	if !results[2].Applies {
		t.Errorf("index 2 = %+v, want the answered verdict salvaged", results[2])
	}
}

// TestParseTagAuditResponse_MissingAppliesIsNotARejection pins why the criteria fields are pointers: an
// entry the judge left unterminated must read as missing, never as a not-applies verdict that a fill would
// quietly skip.
func TestParseTagAuditResponse_MissingAppliesIsNotARejection(t *testing.T) {
	unterminated := `{"tags":[{"index":0,"defer":false}]}`
	_, err := ParseTagAuditResponse(unterminated, 1)
	incomplete, ok := errors.AsType[*IncompleteReplyError](err)
	if !ok {
		t.Fatalf("error type = %T, want *IncompleteReplyError for the unterminated entry", err)
	}
	if len(incomplete.Missing) != 1 || incomplete.Missing[0] != 0 {
		t.Errorf("Missing = %v, want [0]", incomplete.Missing)
	}
}

func TestParseTagAuditResponse_BareArrayPositional(t *testing.T) {
	bare := `[
		{"applies":true,"defer":false},
		{"applies":false,"defer":true}
	]`
	results, err := ParseTagAuditResponse(bare, 2)
	if err != nil {
		t.Fatalf("a bare array envelope must parse: %v", err)
	}
	if !reflect.DeepEqual(results, []TagAuditResult{
		{Index: 0, Applies: true, Defer: false},
		{Index: 1, Applies: false, Defer: true},
	}) {
		t.Errorf("results = %+v, want the entries placed by position", results)
	}
}

func TestParseTagAuditResponse_OutOfRangeIndexIsIgnored(t *testing.T) {
	reply := `{"tags":[{"index":7,"applies":true,"defer":false}]}`
	_, err := ParseTagAuditResponse(reply, 1)
	incomplete, ok := errors.AsType[*IncompleteReplyError](err)
	if !ok {
		t.Fatalf("error type = %T, want *IncompleteReplyError: the reply answered no real candidate", err)
	}
	if len(incomplete.Missing) != 1 || incomplete.Missing[0] != 0 {
		t.Errorf("Missing = %v, want [0]", incomplete.Missing)
	}
}

// TestParseTagAuditResponse_ExplicitIndexWinsOverPosition pins the placement the salvage path depends on:
// a truncated reply shifts every entry away from its position, so the entry's own index — not its place in
// the array — is what lands it in the results slot the driver keys by dimension.
func TestParseTagAuditResponse_ExplicitIndexWinsOverPosition(t *testing.T) {
	shifted := `{"tags":[
		{"index":2,"applies":true,"defer":false},
		{"index":0,"applies":false,"defer":false}
	]}`
	results, err := ParseTagAuditResponse(shifted, 3)
	incomplete, ok := errors.AsType[*IncompleteReplyError](err)
	if !ok || len(incomplete.Missing) != 1 || incomplete.Missing[0] != 1 {
		t.Fatalf("error = %v, want a single missing index 1", err)
	}
	if !results[2].Applies || results[0].Applies {
		t.Errorf("results = %+v, want index 2 salvaged as applies and index 0 as not applies", results)
	}
}

// TestTagAuditCriteria_PointerDiscipline covers the persisted shape: defer is optional so an omitted key
// reads as false, while an omitted applies leaves the criteria incomplete.
func TestTagAuditCriteria_PointerDiscipline(t *testing.T) {
	var omittedDefer TagAuditCriteria
	applies := true
	omittedDefer.Applies = &applies

	if !omittedDefer.Complete() {
		t.Error("criteria with applies must be complete")
	}
	result, ok := omittedDefer.Result()
	if !ok || !result.Applies || result.Defer {
		t.Errorf("Result = %+v,%v, want applies with defer=false when the key is omitted", result, ok)
	}

	var unterminated TagAuditCriteria
	if unterminated.Complete() {
		t.Error("criteria without applies must be incomplete")
	}
	if _, ok := unterminated.Result(); ok {
		t.Error("an unterminated criteria must not read as a verdict")
	}
}
