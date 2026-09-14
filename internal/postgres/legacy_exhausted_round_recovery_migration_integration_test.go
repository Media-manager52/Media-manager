package postgres

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestLegacyExhaustedRoundRecoveryMigrationSchedulesDiscovery proves that an
// exhausted, non-terminal workflow with no retry-discovery row is moved onto
// the existing paced-discovery path, while workflows that already have a
// discovery row and workflows with an open round are left untouched.
func TestLegacyExhaustedRoundRecoveryMigrationSchedulesDiscovery(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	schema := "mm_legacy_recovery_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	}()

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	store, err := Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := migrateThrough(ctx, store.Pool, 36); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	clientID, rootID, profileID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,created_at,updated_at) VALUES($1,'altmount','Legacy Recovery AltMount',true,true,'{}',$2,$2)`, clientID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO library_roots(id,media_kind,name,destination_root,plex_visible_root,source_visible_root,is_default,enabled) VALUES($1,'movie','Legacy Recovery Movies','/library','/library','/output',true,true)`, rootID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO quality_profiles(id,media_kind,name,policy_version,policy,upgrades_enabled,is_default,enabled) VALUES($1,'movie','Legacy Recovery Profile',1,'{}',true,true,true)`, profileID.String()); err != nil {
		t.Fatal(err)
	}

	seed := func(title string) (uuid.UUID, uuid.UUID) {
		t.Helper()
		mediaID, workflowID := uuid.NewV7(), uuid.NewV7()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO media_items(id,kind,title,year,revision,created_at,updated_at) VALUES($1,'movie',$2,2026,1,$3,$3)`, mediaID.String(), title, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,attention_code,attention_detail,created_at,updated_at) VALUES($1,$2,'needs_attention',$3,$4,$5,'default',1,$6,'legacy attention',$7,$7)`, workflowID.String(), mediaID.String(), rootID.String(), profileID.String(), clientID.String(), "subtitle_requirements_unverified", now); err != nil {
			t.Fatal(err)
		}
		return mediaID, workflowID
	}
	exhaustRound := func(workflowID uuid.UUID, ordinal int) uuid.UUID {
		t.Helper()
		roundID := uuid.NewV7()
		if _, err := store.Pool.Exec(ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at,exhausted_at,closed_at,close_reason) VALUES($1,$2,$3,'exhausted',2,0,1,'{}','',$4,$4,$4,'subtitle_requirements_unverified')`, roundID.String(), workflowID.String(), ordinal, now); err != nil {
			t.Fatal(err)
		}
		return roundID
	}

	_, strandedID := seed("Legacy Stranded")
	exhaustRound(strandedID, 1)

	_, discoveredID := seed("Legacy Already Discovered")
	discoveredRound := exhaustRound(discoveredID, 1)
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET state='waiting',attention_code='round_exhausted_waiting_new_identity' WHERE id=$1`, discoveredID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO workflow_retry_discovery(workflow_id,exhausted_round_id,state,rescan_generation,next_rescan_at,complete_no_new_scans,created_at,updated_at) VALUES($1,$2,'waiting',7,$3,0,$3,$3)`, discoveredID.String(), discoveredRound.String(), now.Add(6*time.Hour)); err != nil {
		t.Fatal(err)
	}

	_, openID := seed("Legacy Still Open")
	openRoundID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO acquisition_rounds(id,workflow_id,round_ordinal,state,attempt_limit_snapshot,novelty_cutoff_sequence,policy_version,policy_snapshot,upgrade_baseline_link_versions,opened_at) VALUES($1,$2,1,'open',2,0,1,'{}','',$3)`, openRoundID.String(), openID.String(), now); err != nil {
		t.Fatal(err)
	}

	if err := migrateThrough(ctx, store.Pool, 37); err != nil {
		t.Fatal(err)
	}

	var strandedState, strandedCode string
	var strandedDiscovery, strandedGeneration, strandedEvents, strandedTransitions int
	if err := store.Pool.QueryRow(ctx, `SELECT
		(SELECT state FROM workflows WHERE id=$1),
		(SELECT COALESCE(attention_code,'') FROM workflows WHERE id=$1),
		(SELECT count(*) FROM workflow_retry_discovery WHERE workflow_id=$1),
		(SELECT COALESCE(max(rescan_generation),0) FROM workflow_retry_discovery WHERE workflow_id=$1),
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND aggregate_type='workflow_retry_discovery' AND event_type='workflow.discovery_rescan_requested'),
		(SELECT count(*) FROM workflow_transitions WHERE workflow_id=$1 AND to_state='waiting' AND actor='migration')`, strandedID.String()).Scan(&strandedState, &strandedCode, &strandedDiscovery, &strandedGeneration, &strandedEvents, &strandedTransitions); err != nil {
		t.Fatal(err)
	}
	if strandedState != "waiting" || strandedCode != "round_exhausted_waiting_new_identity" || strandedDiscovery != 1 || strandedGeneration != 1 || strandedEvents != 1 || strandedTransitions != 1 {
		t.Fatalf("stranded recovery state=%q code=%q discovery=%d generation=%d events=%d transitions=%d", strandedState, strandedCode, strandedDiscovery, strandedGeneration, strandedEvents, strandedTransitions)
	}

	var discoveredGeneration int
	var discoveredCount int
	if err := store.Pool.QueryRow(ctx, `SELECT COALESCE(max(rescan_generation),0),count(*) FROM workflow_retry_discovery WHERE workflow_id=$1`, discoveredID.String()).Scan(&discoveredGeneration, &discoveredCount); err != nil {
		t.Fatal(err)
	}
	if discoveredCount != 1 || discoveredGeneration != 7 {
		t.Fatalf("existing discovery was rewritten: count=%d generation=%d", discoveredCount, discoveredGeneration)
	}

	var openDiscovery, openState int
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflow_retry_discovery WHERE workflow_id=$1),(SELECT count(*) FROM workflows WHERE id=$1 AND state='needs_attention')`, openID.String()).Scan(&openDiscovery, &openState); err != nil {
		t.Fatal(err)
	}
	if openDiscovery != 0 || openState != 1 {
		t.Fatalf("open round was changed: discovery=%d needs_attention=%d", openDiscovery, openState)
	}

	var version int64
	if err := store.Pool.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 37 {
		t.Fatalf("schema version=%d, want 37", version)
	}
}
