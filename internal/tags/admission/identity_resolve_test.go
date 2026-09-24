package admission

import (
	"context"
	"database/sql"
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
