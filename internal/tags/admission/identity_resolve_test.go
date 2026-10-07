package admission

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/wgomg/edub-kushim/internal/config"
	"github.com/wgomg/edub-kushim/internal/database"
	"github.com/wgomg/edub-kushim/internal/llm/analyzer"
	"github.com/wgomg/edub-kushim/internal/log"
	"github.com/wgomg/edub-kushim/internal/tags"
	"github.com/wgomg/edub-kushim/internal/tags/curation"
	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/tools"
	"github.com/wgomg/edub-kushim/internal/types"
)

func newIdentityResolver(t *testing.T, adjudicatorEnabled bool) (*IdentityResolver, *database.Client, *mockAdjudicator) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	client := database.NewTestClient(t)
	t.Cleanup(func() { client.DB().Close() })
	database.ResetTestDatabase(client.DB())
	if _, err := client.DB().ExecContext(context.Background(), "DELETE FROM tag_identity_event"); err != nil {
		t.Fatalf("clear tag_identity_event: %v", err)
	}

	logger := log.NewDiscardLogger()
	adj := &mockAdjudicator{}

	runner := &tools.Runner{}
	setUnexportedField(t, runner, "logger", logger)
	setUnexportedField(t, runner, "config", &config.Config{
		Enricher: config.EnricherConfig{
			TagAdjudicator: config.TagAdjudicatorConfig{Timeout: 0},
		},
	})
	setUnexportedField(t, runner, "tagAdjudicator", adj)
	setUnexportedField(t, runner, "tagIdentityJudge", adj)

	cfg := &config.Config{
		Enricher: config.EnricherConfig{
			TagAdjudicator: config.TagAdjudicatorConfig{
				Enabled: adjudicatorEnabled,
				Timeout: 0,
				Llm:     config.LlmConfig{Model: "test-model"},
			},
		},
	}
	tagSvc := curation.NewTag(client, testutil.NewTestLogger(), testutil.NewMockEmbedder())

	resolver := NewIdentityResolver(func() *config.Config { return cfg }, logger, client, tagSvc, runner)
	return resolver, client, adj
}

func seedPendingIdentityEvent(t *testing.T, client *database.Client, kind, candidate, partner string) {
	t.Helper()
	if _, err := client.Queries.InsertTagIdentityEvent(context.Background(), database.InsertTagIdentityEventParams{
		Kind:           kind,
		Candidate:      candidate,
		Partner:        sql.NullString{String: partner, Valid: partner != ""},
		Criteria:       []byte("{}"),
		RuleSetVersion: tags.IdentityRuleSetVersion,
	}); err != nil {
		t.Fatalf("seed pending identity event: %v", err)
	}
}

func pendingIdentityCount(t *testing.T, client *database.Client) int64 {
	t.Helper()
	count, err := client.Queries.CountPendingTagIdentityEvents(context.Background())
	testutil.AssertNoError(t, err, "count pending identity events")
	return count
}

func TestResolvePending_Fold(t *testing.T) {
	resolver, client, adj := newIdentityResolver(t, true)
	ctx := context.Background()

	database.MustCreateTag(t, client.Queries, "buildings")
	seedPendingIdentityEvent(t, client, tags.IdentityKindBaseForm, "buildings", "")
	seedPendingIdentityEvent(t, client, tags.IdentityKindFoldGate, "buildings", "")
	seedPendingIdentityEvent(t, client, tags.IdentityKindConventionalForm, "buildings", "")

	adj.identityFn = func(_ context.Context, candidates []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
		results := make([]analyzer.TagIdentityResult, len(candidates))
		for i := range candidates {
			results[i] = analyzer.TagIdentityResult{
				Index:                   i,
				FoldToBase:              true,
				FoldPreservesDenotation: true,
				ConventionalForm:        true,
			}
		}
		return results, nil
	}

	report, err := resolver.ResolvePending(ctx, 100)
	testutil.AssertNoError(t, err, "resolve pending")
	testutil.AssertEqual(t, report.Pending, 3, "pending rows read")
	testutil.AssertEqual(t, report.Resolved, 3, "criterion rows resolved")
	testutil.AssertEqual(t, report.Folded, 1, "confirmed fold applied")
	testutil.AssertEqual(t, report.Failed, 0, "no failures")
	testutil.AssertEqual(t, adj.identityCall, 1, "one batched judge call")

	if _, err := client.Queries.GetTagByName(ctx, "buildings"); err == nil {
		t.Error("unreduced tag row must be gone after the fold")
	}
	if _, err := client.Queries.GetTagByName(ctx, "building"); err != nil {
		t.Errorf("folded tag missing: %v", err)
	}
	testutil.AssertEqual(t, pendingIdentityCount(t, client), int64(0), "no pending rows left")
}

func TestResolvePending_SameIntentPair(t *testing.T) {
	resolver, client, adj := newIdentityResolver(t, true)
	ctx := context.Background()

	candidateID := database.MustCreateTag(t, client.Queries, "colonization")
	database.MustCreateTag(t, client.Queries, "settler colonialism")
	seedPendingIdentityEvent(t, client, tags.IdentityKindSameIntent, "colonization", "settler colonialism")

	adj.adjudicateFn = func(_ context.Context, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
		results := make([]analyzer.TagVerdictResult, len(pairs))
		for i := range pairs {
			results[i] = analyzer.TagVerdictResult{
				Index:     i,
				Verdict:   types.TagVerdicts.Same,
				Direction: analyzer.TagDirectionQuery,
			}
		}
		return results, nil
	}

	report, err := resolver.ResolvePending(ctx, 100)
	testutil.AssertNoError(t, err, "resolve pending")
	testutil.AssertEqual(t, report.Pending, 1, "pending pair read")
	testutil.AssertEqual(t, report.Resolved, 1, "pair verdict recorded")
	testutil.AssertEqual(t, report.Aliased, 1, "loser aliased onto the winner")
	testutil.AssertEqual(t, adj.called, 1, "one adjudicator call")

	if _, err := client.Queries.GetTagByName(ctx, "settler colonialism"); err == nil {
		t.Error("loser tag row must be removed")
	}
	alias, err := client.Queries.GetTagAliasByName(ctx, "settler colonialism")
	testutil.AssertNoError(t, err, "loser name becomes an alias")
	testutil.AssertEqual(t, alias.CanonicalTagID, candidateID, "alias points at the winner")
	testutil.AssertEqual(t, pendingIdentityCount(t, client), int64(0), "no pending rows left")
}

func TestResolvePending_PartialCandidateReply(t *testing.T) {
	resolver, client, adj := newIdentityResolver(t, true)

	for _, name := range []string{"buildings", "monuments"} {
		seedPendingIdentityEvent(t, client, tags.IdentityKindBaseForm, name, "")
		seedPendingIdentityEvent(t, client, tags.IdentityKindFoldGate, name, "")
		seedPendingIdentityEvent(t, client, tags.IdentityKindConventionalForm, name, "")
	}

	adj.identityFn = func(_ context.Context, candidates []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
		results := make([]analyzer.TagIdentityResult, len(candidates))
		for i := range candidates {
			results[i] = analyzer.TagIdentityResult{
				Index:                   i,
				FoldPreservesDenotation: true,
				ConventionalForm:        true,
			}
		}
		return results, &analyzer.IncompleteReplyError{Missing: []int{1}}
	}

	report, err := resolver.ResolvePending(context.Background(), 100)
	testutil.AssertNoError(t, err, "resolve pending")
	testutil.AssertEqual(t, report.Pending, 6, "pending rows read")
	testutil.AssertEqual(t, report.Resolved, 3, "the returned candidate's rows are resolved")
	testutil.AssertEqual(t, report.Deferred, 1, "the missing candidate is deferred")
	testutil.AssertEqual(t, report.Failed, 0, "a partial reply is not a failure")
	testutil.AssertEqual(t, adj.identityCall, 1, "an incomplete reply is not retried")
	testutil.AssertEqual(t, pendingIdentityCount(t, client), int64(3), "the missing candidate's rows stay pending")
}

func TestResolvePending_PartialPairReply(t *testing.T) {
	resolver, client, adj := newIdentityResolver(t, true)

	seedPendingIdentityEvent(t, client, tags.IdentityKindSameIntent, "colonization", "settler colonialism")
	seedPendingIdentityEvent(t, client, tags.IdentityKindSameIntent, "labour", "labor")

	adj.adjudicateFn = func(_ context.Context, pairs []analyzer.TagPairEvidence) ([]analyzer.TagVerdictResult, error) {
		results := make([]analyzer.TagVerdictResult, len(pairs))
		results[0] = analyzer.TagVerdictResult{Index: 0, Verdict: types.TagVerdicts.Variant}
		return results, &analyzer.IncompleteReplyError{Missing: []int{1}}
	}

	report, err := resolver.ResolvePending(context.Background(), 100)
	testutil.AssertNoError(t, err, "resolve pending")
	testutil.AssertEqual(t, report.Pending, 2, "pending pairs read")
	testutil.AssertEqual(t, report.Resolved, 1, "the returned pair's verdict is recorded")
	testutil.AssertEqual(t, report.Deferred, 1, "the missing pair is deferred")
	testutil.AssertEqual(t, report.Aliased, 0, "a variant verdict creates no alias")
	testutil.AssertEqual(t, report.Failed, 0, "a partial reply is not a failure")
	testutil.AssertEqual(t, adj.called, 1, "an incomplete reply is not retried")
	testutil.AssertEqual(t, pendingIdentityCount(t, client), int64(1), "the missing pair stays pending")
}

func TestResolvePending_AdjudicatorDisabled(t *testing.T) {
	resolver, client, adj := newIdentityResolver(t, false)
	seedPendingIdentityEvent(t, client, tags.IdentityKindSameIntent, "colonization", "settler colonialism")

	report, err := resolver.ResolvePending(context.Background(), 100)
	testutil.AssertNoError(t, err, "resolve pending with the adjudicator off")
	testutil.AssertEqual(t, report.Pending, 1, "pending reported")
	testutil.AssertEqual(t, report.Resolved, 0, "nothing resolved")
	testutil.AssertEqual(t, report.Aliased, 0, "no disposition applied")
	testutil.AssertEqual(t, adj.called, 0, "no adjudication call")
	testutil.AssertEqual(t, adj.identityCall, 0, "no judge call")
	testutil.AssertEqual(t, pendingIdentityCount(t, client), int64(1), "evidence stays pending for the next run")
}

func TestResolvePending_DerivationalFoldOntoStoreNoun(t *testing.T) {
	resolver, client, adj := newIdentityResolver(t, true)
	ctx := context.Background()

	database.MustCreateTag(t, client.Queries, "political")
	// the noun comes from seed-tags.sql, which every reset reloads;
	// creating it again would collide with the seeded row
	storeNoun, err := client.Queries.GetTagByName(ctx, "politics")
	testutil.AssertNoError(t, err, "seeded store noun politics")
	politicsID := storeNoun.ID
	seedPendingIdentityEvent(t, client, tags.IdentityKindBaseForm, "political", "")
	seedPendingIdentityEvent(t, client, tags.IdentityKindFoldGate, "political", "")
	seedPendingIdentityEvent(t, client, tags.IdentityKindConventionalForm, "political", "")

	var seen []analyzer.TagIdentityCandidate
	adj.identityFn = func(_ context.Context, candidates []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
		seen = append(seen, candidates...)
		results := make([]analyzer.TagIdentityResult, len(candidates))
		for i := range candidates {
			results[i] = analyzer.TagIdentityResult{
				Index:                   i,
				FoldToBase:              true,
				FoldPreservesDenotation: true,
				ConventionalForm:        true,
			}
		}
		return results, nil
	}

	report, err := resolver.ResolvePending(ctx, 100)
	testutil.AssertNoError(t, err, "resolve pending")
	testutil.AssertEqual(t, report.Pending, 3, "pending rows read")
	testutil.AssertEqual(t, report.Resolved, 3, "criterion rows resolved")
	testutil.AssertEqual(t, report.Folded, 1, "derivational fold applied")
	testutil.AssertEqual(t, report.Failed, 0, "no failures")

	// one proposal feeds the judge input, the stored criteria and the apply
	// site, so every consumer must see the same store noun
	if len(seen) != 1 {
		t.Fatalf("judge candidates = %d, want 1", len(seen))
	}
	testutil.AssertEqual(t, seen[0].Term, "political", "the pending adjective reaches the judge")
	testutil.AssertEqual(t, seen[0].ProposedFold, "politics", "the store noun is the proposed fold the judge decides")

	rows, err := client.Queries.LatestTagIdentityEventsForCandidates(ctx, database.LatestTagIdentityEventsForCandidatesParams{
		RuleSetVersion: tags.IdentityRuleSetVersion,
		Candidates:     []string{"political"},
	})
	testutil.AssertNoError(t, err, "read resolved identity rows")
	if len(rows) != 3 {
		t.Fatalf("resolved rows = %d, want 3", len(rows))
	}
	var criteria struct {
		ProposedFold string `json:"proposed_fold"`
	}
	if err := json.Unmarshal(rows[0].Criteria, &criteria); err != nil {
		t.Fatalf("unmarshal criteria: %v", err)
	}
	testutil.AssertEqual(t, criteria.ProposedFold, "politics", "stored criteria carry the same proposal")

	if _, err := client.Queries.GetTagByName(ctx, "political"); err == nil {
		t.Error("unreduced adjective row must be gone after the fold")
	}
	if _, err := client.Queries.GetTagByName(ctx, "politics"); err != nil {
		t.Errorf("store noun missing: %v", err)
	}
	alias, err := client.Queries.GetTagAliasByName(ctx, "political")
	testutil.AssertNoError(t, err, "adjective name becomes an alias of the noun")
	testutil.AssertEqual(t, alias.CanonicalTagID, politicsID, "alias points at the store noun")
	testutil.AssertEqual(t, pendingIdentityCount(t, client), int64(0), "no pending rows left")
}

func TestResolvePending_FoldTargetDeleted(t *testing.T) {
	resolver, client, adj := newIdentityResolver(t, true)
	ctx := context.Background()

	database.MustCreateTag(t, client.Queries, "buildings")
	seedPendingIdentityEvent(t, client, tags.IdentityKindBaseForm, "buildings", "")
	seedPendingIdentityEvent(t, client, tags.IdentityKindFoldGate, "buildings", "")
	seedPendingIdentityEvent(t, client, tags.IdentityKindConventionalForm, "buildings", "")
	if err := client.Queries.UpsertDeletedTag(ctx, database.UpsertDeletedTagParams{
		Name: "building",
		Kind: curation.DeletedTagKindBlocked,
	}); err != nil {
		t.Fatalf("seed deleted fold target: %v", err)
	}

	adj.identityFn = func(_ context.Context, candidates []analyzer.TagIdentityCandidate) ([]analyzer.TagIdentityResult, error) {
		results := make([]analyzer.TagIdentityResult, len(candidates))
		for i := range candidates {
			results[i] = analyzer.TagIdentityResult{
				Index:                   i,
				FoldToBase:              true,
				FoldPreservesDenotation: true,
				ConventionalForm:        true,
			}
		}
		return results, nil
	}

	report, err := resolver.ResolvePending(ctx, 100)
	testutil.AssertNoError(t, err, "resolve pending")
	testutil.AssertEqual(t, report.Resolved, 3, "criterion rows still resolved")
	testutil.AssertEqual(t, report.Folded, 0, "deleted target is not resurrected")

	if _, err := client.Queries.GetTagByName(ctx, "buildings"); err != nil {
		t.Errorf("unreduced tag must survive: %v", err)
	}
	if _, err := client.Queries.GetTagByName(ctx, "building"); err == nil {
		t.Error("deleted fold target must stay deleted")
	}
}
