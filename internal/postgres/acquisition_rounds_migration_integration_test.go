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

func TestAcquisitionRoundMigrationPreservesHistoricalDuplicateIdentities(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	schema := "mm_round_migration_" + strings.ReplaceAll(uuid.New().String(), "-", "")
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
	if err := migrateThrough(ctx, store.Pool, 31); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	clientID, rootID, profileID, mediaID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	workflowID, runID, observationID, candidateID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	jobOneID, jobTwoID := uuid.NewV7(), uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,created_at,updated_at) VALUES($1,'altmount','Migration AltMount',true,true,'{}',$2,$2)`, clientID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO library_roots(id,media_kind,name,destination_root,plex_visible_root,source_visible_root,is_default,enabled) VALUES($1,'movie','Migration Movies','/library','/library','/output',true,true)`, rootID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO quality_profiles(id,media_kind,name,policy_version,policy,upgrades_enabled,is_default,enabled) VALUES($1,'movie','Migration Profile',1,'{}',true,true,true)`, profileID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO media_items(id,kind,title,year,revision,created_at,updated_at) VALUES($1,'movie','Migration Movie',2026,1,$2,$2)`, mediaID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO workflows(id,media_id,state,root_id,quality_profile_id,download_client_id,variant,revision,created_at,updated_at) VALUES($1,$2,'needs_attention',$3,$4,$5,'default',1,$6,$6)`, workflowID.String(), mediaID.String(), rootID.String(), profileID.String(), clientID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO search_runs(id,workflow_id,mode,query_fingerprint,state,policy_version,started_at,completed_at) VALUES($1,$2,'targeted',$3,'complete',1,$4,$4)`, runID.String(), workflowID.String(), []byte{1}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,'migration','same-release','Migration.Release',$3,'[]','[]','test','[]',$4)`, observationID.String(), clientID.String(), []byte{2}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_candidates(id,workflow_id,search_run_id,observation_id,indexer_instance_id,native_key,title,download_locator_ref,created_at) VALUES($1,$2,$3,$4,$5,'same-release','Migration.Release','native:same-release',$6)`, candidateID.String(), workflowID.String(), runID.String(), observationID.String(), clientID.String(), now); err != nil {
		t.Fatal(err)
	}
	packageIdentity := make([]byte, 32)
	packageIdentity[0] = 42
	if _, err := store.Pool.Exec(ctx, `INSERT INTO altmount_jobs(id,workflow_id,candidate_id,altmount_instance_id,attempt_generation,upload_name,state,submission_started_at,last_reconciled_at,error_code,error_detail,revision,nzb_package_sha256) VALUES
		($1,$3,$4,$5,1,'historical-one.nzb','needs_attention',$6,$6,'output_verification_failed','first historical failure',1,$7),
		($2,$3,$4,$5,2,'historical-two.nzb','needs_attention',$6,$6,'output_verification_failed','second historical failure',1,$7)`, jobOneID.String(), jobTwoID.String(), workflowID.String(), candidateID.String(), clientID.String(), now, packageIdentity); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE workflows SET selected_release_id=$2,grab_attempt_id=$3 WHERE id=$1`, workflowID.String(), candidateID.String(), jobTwoID.String()); err != nil {
		t.Fatal(err)
	}

	if err := migrateThrough(ctx, store.Pool, 32); err != nil {
		t.Fatal(err)
	}
	var schemaVersion int64
	var jobs, rounds, attemptedReleases, submittedPackages, retryDiscovery int
	if err := store.Pool.QueryRow(ctx, `SELECT
		(SELECT COALESCE(max(version),0) FROM schema_migrations),
		(SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1),
		(SELECT count(*) FROM acquisition_rounds WHERE workflow_id=$1 AND state='exhausted'),
		(SELECT count(*) FROM workflow_attempted_releases WHERE workflow_id=$1),
		(SELECT count(*) FROM workflow_submitted_packages WHERE workflow_id=$1),
		(SELECT count(*) FROM workflow_retry_discovery WHERE workflow_id=$1)`, workflowID.String()).Scan(&schemaVersion, &jobs, &rounds, &attemptedReleases, &submittedPackages, &retryDiscovery); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != 32 || jobs != 2 || rounds != 1 || attemptedReleases != 1 || submittedPackages != 1 || retryDiscovery != 0 {
		t.Fatalf("schema=%d jobs=%d exhausted_rounds=%d attempted=%d packages=%d retry_discovery=%d", schemaVersion, jobs, rounds, attemptedReleases, submittedPackages, retryDiscovery)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE altmount_jobs SET state='queued' WHERE workflow_id=$1`, workflowID.String()); err == nil {
		t.Fatal("database allowed two active acquisitions in one workflow")
	}
	var activeJobs int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM altmount_jobs WHERE workflow_id=$1 AND state IN ('intent','submitting','transport_unknown','queued','processing','streaming_ready','cancel_requested')`, workflowID.String()).Scan(&activeJobs); err != nil || activeJobs != 0 {
		t.Fatalf("failed uniqueness statement changed historical jobs: active=%d err=%v", activeJobs, err)
	}
	if err := migrateThrough(ctx, store.Pool, 32); err != nil {
		t.Fatalf("repeated migration: %v", err)
	}
}
