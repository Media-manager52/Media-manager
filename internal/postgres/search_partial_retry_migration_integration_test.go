package postgres

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/workflows"
	"uuid"
)

func seedPopulatedSchema49(t *testing.T, ctx context.Context, store *Store) (workflows.Workflow, SearchContext, SearchPartialRetry, []byte) {
	t.Helper()
	plex, err := store.CreateIntegration(ctx, Integration{Kind: "plex", Name: "Migration Plex", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://plex.invalid:32400","allow_http":true}`), SecretRef: "MIGRATION_PLEX"})
	if err != nil {
		t.Fatal(err)
	}
	altMount, err := store.CreateIntegration(ctx, Integration{Kind: "altmount", Name: "Migration AltMount", Enabled: true, Default: true, Config: json.RawMessage(`{"base_url":"http://altmount.invalid:8080","allow_http":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	indexer, err := store.CreateIntegration(ctx, Integration{Kind: "newznab", Name: "Migration Indexer", Config: json.RawMessage(`{"base_url":"https://indexer.invalid/api"}`), SecretRef: "MIGRATION_INDEXER"})
	if err != nil {
		t.Fatal(err)
	}
	dialects := newznab.SearchDialects{Movie: newznab.SearchDialect{Mode: "movie", Params: []string{"q", "year"}}, Series: newznab.SearchDialect{Mode: "tvsearch", Params: []string{"q", "season"}}}
	if _, err := store.CompleteNewznabCommission(ctx, indexer.ID, newznab.CapsSnapshot{ServerVersion: "migration", BodyHash: "migration-caps", DefaultLimit: 50, MaximumLimit: 100}, dialects, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateLibraryRoot(ctx, LibraryRoot{MediaKind: catalog.KindMovie, Name: "Migration Movies", DestinationRoot: "/libraries/movies", PlexVisibleRoot: "/libraries/movies", SourceVisibleRoot: "/mnt/altmount", PlexInstanceID: &plex.ID, PlexLibraryID: "1", Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"rules": []releases.Rule{{Name: "Resolution", Fact: "resolution", AllowedValues: []string{"1080p"}, Kind: releases.RuleRequired, Unknown: releases.UnknownHold}}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Migration quality", PolicyVersion: 1, Policy: policy, UpgradesEnabled: true, Default: true, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	year := uint16(2026)
	movie, err := catalog.New(catalog.KindMovie, "Migration Movie", &year, []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "901501", State: catalog.IdentityVerified, Source: "test"}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateManualMedia(ctx, movie, root.ID, profile.ID, altMount.ID, nil, true, "migration-test")
	if err != nil || len(created) != 1 {
		t.Fatalf("create workflow=%+v err=%v", created, err)
	}
	workflow := created[0]
	if workflow.State != workflows.StateSearching {
		workflow, err = store.TransitionWorkflow(ctx, workflow.ID, workflow.Revision, workflows.StateSearching, "migration seed", "test", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
	}
	search, err := store.LoadSearchContext(ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	observation := releases.Observation{ID: uuid.NewV7(), IndexerID: indexer.ID, Operation: "test", NativeGUID: "migration-native", Title: "Migration.Movie.2026.720p.WEB-DL", Facts: []releases.Fact{{Name: "resolution", Values: []string{"720p"}, State: releases.FactObserved}}, BodyHash: "migration-body", ObservedAt: time.Now().UTC(), ParseVersion: "test", Problems: []string{}}
	evaluated := []EvaluatedObservation{{Observation: observation, Evaluation: releases.Evaluation{ObservationID: observation.ID, Verdict: releases.VerdictHeld, Explanations: []releases.Explanation{}}, ProviderResult: true}}
	outcomes := []SearchIndexerOutcome{{IndexerInstanceID: indexer.ID, State: "succeeded"}}
	if _, err := store.PersistSearch(ctx, search, []byte("migration-seed"), evaluated, SearchPersistenceOptions{IndexerOutcomes: outcomes}, time.Now().UTC()); err != nil {
		t.Fatalf("seed schema-49 search rows: %v", err)
	}
	fingerprint := make([]byte, 32)
	for i := range fingerprint {
		fingerprint[i] = byte(i + 1)
	}
	retry := SearchPartialRetry{
		WorkflowID:        workflow.ID,
		RoundID:           search.SearchRoundID,
		Mode:              "targeted",
		IntentFingerprint: fingerprint,
		PlannedIndexerIDs: []uuid.UUID{indexer.ID},
		CompletedIndexers: map[uuid.UUID][]SearchCandidateEvidence{},
	}
	return workflow, search, retry, fingerprint
}

func TestSearchPartialRetryMigrationFromPopulatedSchema49(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store := subtitleIntegrationStore(t, ctx, databaseURL, 49)
	workflow, search, retry, fingerprint := seedPopulatedSchema49(t, ctx, store)
	if search.SearchRoundID == (uuid.UUID{}) {
		t.Fatal("seeded schema-49 round id is missing")
	}

	var baselineVersion, baselineSearchRuns, baselineObservations, baselineCandidates, baselineDecisions int64
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT coalesce(max(version),0) FROM schema_migrations),(SELECT count(*) FROM search_runs WHERE workflow_id=$1),(SELECT count(*) FROM release_observations o JOIN release_candidates c ON c.observation_id=o.id WHERE c.workflow_id=$1),(SELECT count(*) FROM release_candidates WHERE workflow_id=$1),(SELECT count(*) FROM candidate_decisions WHERE workflow_id=$1)`, workflow.ID.String()).Scan(&baselineVersion, &baselineSearchRuns, &baselineObservations, &baselineCandidates, &baselineDecisions); err != nil {
		t.Fatal(err)
	}
	var baselineState string
	var baselineRevision int64
	if err := store.Pool.QueryRow(ctx, `SELECT state,revision FROM workflows WHERE id=$1`, workflow.ID.String()).Scan(&baselineState, &baselineRevision); err != nil {
		t.Fatal(err)
	}
	if baselineVersion != 49 || baselineSearchRuns == 0 || baselineObservations == 0 || baselineCandidates == 0 || baselineDecisions == 0 {
		t.Fatalf("schema-49 seed baseline version=%d runs=%d observations=%d candidates=%d decisions=%d", baselineVersion, baselineSearchRuns, baselineObservations, baselineCandidates, baselineDecisions)
	}

	if err := Migrate(ctx, store.Pool); err != nil {
		t.Fatalf("apply migration 50: %v", err)
	}
	var version int64
	if err := store.Pool.QueryRow(ctx, `SELECT coalesce(max(version),0) FROM schema_migrations`).Scan(&version); err != nil || version != 50 {
		t.Fatalf("schema version=%d err=%v, want 50", version, err)
	}
	var tableExists, fkExists, uniqueExists, expiryIndexExists bool
	if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='search_partial_retries')`).Scan(&tableExists); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='search_partial_retries'::regclass AND contype='f' AND confrelid='acquisition_rounds'::regclass)`).Scan(&fkExists); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='search_partial_retries'::regclass AND contype='u')`).Scan(&uniqueExists); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND tablename='search_partial_retries' AND indexname='search_partial_retries_expiry_idx')`).Scan(&expiryIndexExists); err != nil {
		t.Fatal(err)
	}
	if !tableExists || !fkExists || !uniqueExists || !expiryIndexExists {
		t.Fatalf("migration structure table=%v fk=%v unique=%v expiry=%v", tableExists, fkExists, uniqueExists, expiryIndexExists)
	}
	var columns int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='search_partial_retries'`).Scan(&columns); err != nil || columns != 10 {
		t.Fatalf("migration columns=%d err=%v, want 10", columns, err)
	}

	var afterVersion, afterSearchRuns, afterObservations, afterCandidates, afterDecisions int64
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT coalesce(max(version),0) FROM schema_migrations),(SELECT count(*) FROM search_runs WHERE workflow_id=$1),(SELECT count(*) FROM release_observations o JOIN release_candidates c ON c.observation_id=o.id WHERE c.workflow_id=$1),(SELECT count(*) FROM release_candidates WHERE workflow_id=$1),(SELECT count(*) FROM candidate_decisions WHERE workflow_id=$1)`, workflow.ID.String()).Scan(&afterVersion, &afterSearchRuns, &afterObservations, &afterCandidates, &afterDecisions); err != nil {
		t.Fatal(err)
	}
	var afterState string
	var afterRevision int64
	if err := store.Pool.QueryRow(ctx, `SELECT state,revision FROM workflows WHERE id=$1`, workflow.ID.String()).Scan(&afterState, &afterRevision); err != nil {
		t.Fatal(err)
	}
	if afterVersion != 50 || afterSearchRuns != baselineSearchRuns || afterObservations != baselineObservations || afterCandidates != baselineCandidates || afterDecisions != baselineDecisions || afterState != baselineState || afterRevision != baselineRevision {
		t.Fatalf("schema-49 data changed: before version=%d runs=%d obs=%d cand=%d dec=%d state=%s rev=%d; after version=%d runs=%d obs=%d cand=%d dec=%d state=%s rev=%d", baselineVersion, baselineSearchRuns, baselineObservations, baselineCandidates, baselineDecisions, baselineState, baselineRevision, afterVersion, afterSearchRuns, afterObservations, afterCandidates, afterDecisions, afterState, afterRevision)
	}

	now := time.Now().UTC()
	stateID := uuid.NewV7()
	expires := now.Add(7 * 24 * time.Hour)
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_partial_retries(id,workflow_id,round_id,mode,intent_fingerprint,planned_indexer_ids,completed_indexers,created_at,updated_at,expires_at) VALUES($1,$2,$3,'targeted',$4,$5,'{}',$6,$6,$7)`, stateID.String(), retry.WorkflowID.String(), retry.RoundID.String(), fingerprint, []string{retry.PlannedIndexerIDs[0].String()}, now, expires); err != nil {
		t.Fatalf("insert valid migrated state: %v", err)
	}
	loaded, err := store.LoadSearchPartialRetry(ctx, retry.WorkflowID, retry.RoundID, "targeted", fingerprint, now)
	if err != nil || loaded.ID != stateID || len(loaded.PlannedIndexerIDs) != 1 {
		t.Fatalf("load migrated state=%+v err=%v", loaded, err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_partial_retries(id,workflow_id,round_id,mode,intent_fingerprint,planned_indexer_ids,completed_indexers,created_at,updated_at,expires_at) VALUES($1,$2,$3,'targeted',$4,$5,'{}',$6,$6,$7)`, uuid.NewV7().String(), retry.WorkflowID.String(), retry.RoundID.String(), fingerprint, []string{retry.PlannedIndexerIDs[0].String()}, now, expires); err == nil {
		t.Fatal("duplicate migrated state intent unexpectedly accepted")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE search_partial_retries SET completed_indexers=$2 WHERE id=$1`, stateID.String(), `{"`+retry.PlannedIndexerIDs[0].String()+`":[]}`); err != nil {
		t.Fatalf("update migrated state: %v", err)
	}
	loaded, err = store.LoadSearchPartialRetry(ctx, retry.WorkflowID, retry.RoundID, "targeted", fingerprint, now)
	if err != nil || len(loaded.CompletedIndexers) != 1 {
		t.Fatalf("updated migrated state=%+v err=%v", loaded, err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_partial_retries(id,workflow_id,round_id,mode,intent_fingerprint,planned_indexer_ids,completed_indexers,created_at,updated_at,expires_at) VALUES($1,$2,$3,'targeted',$4,$5,'{}',$6,$6,$7)`, uuid.NewV7().String(), workflow.ID.String(), uuid.NewV7().String(), fingerprint, []string{retry.PlannedIndexerIDs[0].String()}, now, expires); err == nil {
		t.Fatal("invalid round reference unexpectedly accepted")
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_partial_retries(id,workflow_id,round_id,mode,intent_fingerprint,planned_indexer_ids,completed_indexers,created_at,updated_at,expires_at) VALUES($1,$2,$3,'targeted',$4,$5,'{}',$6,$6,$7)`, uuid.NewV7().String(), uuid.NewV7().String(), retry.RoundID.String(), fingerprint, []string{retry.PlannedIndexerIDs[0].String()}, now, expires); err == nil {
		t.Fatal("invalid workflow reference unexpectedly accepted")
	}
	if err := store.DeleteSearchPartialRetry(ctx, retry.WorkflowID, retry.RoundID, "targeted"); err != nil {
		t.Fatalf("delete migrated state: %v", err)
	}
	var remaining int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM search_partial_retries WHERE workflow_id=$1`, workflow.ID.String()).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("remaining migrated states=%d err=%v", remaining, err)
	}
	if err := store.Ready(ctx); err != nil {
		t.Fatalf("schema-50 application startup readiness: %v", err)
	}
}
