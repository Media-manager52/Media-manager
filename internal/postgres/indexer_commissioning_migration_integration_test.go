package postgres

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"uuid"
)

func TestIndexerCommissioningMigrationIsAdditiveAndEnforcesHistory(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	schema := "mm_indexer_commissioning_migration_" + strings.ReplaceAll(uuid.New().String(), "-", "")
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
	if err := migrateThrough(ctx, store.Pool, 33); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID, shadowIndexerID, otherID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES
		($1,'newznab','Commissioning fixture',false,false,$3,'COMMISSIONING_FIXTURE_KEY',$5,$5),
		($2,'altmount','Unrelated fixture',false,false,$4,NULL,$5,$5),
		($6,'newznab','Shadow fixture',false,false,$7,'SHADOW_FIXTURE_KEY',$5,$5)`, indexerID.String(), otherID.String(),
		`{"base_url":"https://commissioning.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000],"category_selection_review_required":false}`,
		`{"unchanged":true}`, now, shadowIndexerID.String(),
		`{"base_url":"https://shadow.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000],"category_selection_review_required":false}`); err != nil {
		t.Fatal(err)
	}
	var endpointDigest []byte
	if err := store.Pool.QueryRow(ctx, `SELECT media_manager_newznab_endpoint_config_sha256(config,secret_ref) FROM integration_instances WHERE id=$1`, indexerID.String()).Scan(&endpointDigest); err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateIndexerCapabilityProfile(ctx, indexerID, activatableCapabilityProfileDocument(t), endpointDigest, now.Add(time.Second))
	if err != nil || profile.LifecycleState != "draft" {
		t.Fatalf("seed schema-33 profile: state=%q err=%v", profile.LifecycleState, err)
	}
	before := commissioningSchema33State(t, ctx, store)
	if err := migrateThrough(ctx, store.Pool, 34); err != nil {
		t.Fatal(err)
	}
	after := commissioningSchema33State(t, ctx, store)
	if before.integrationCount != after.integrationCount || before.newznabCount != after.newznabCount || before.enabledCount != after.enabledCount || before.profileCount != after.profileCount || before.activeProfileCount != after.activeProfileCount || before.operationStatusCount != after.operationStatusCount || before.auditEventCount != after.auditEventCount || !bytes.Equal(before.integrationStateSHA256, after.integrationStateSHA256) || !bytes.Equal(before.profileStateSHA256, after.profileStateSHA256) {
		t.Fatal("migration 0034 changed existing schema-33 integration or capability-profile state")
	}

	var schemaVersion int64
	var runs, steps int
	if err := store.Pool.QueryRow(ctx, `SELECT
		(SELECT COALESCE(max(version),0) FROM schema_migrations),
		(SELECT count(*) FROM indexer_commissioning_runs),
		(SELECT count(*) FROM indexer_commissioning_steps)`).Scan(&schemaVersion, &runs, &steps); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != 34 || runs != 0 || steps != 0 {
		t.Fatalf("schema=%d runs=%d steps=%d", schemaVersion, runs, steps)
	}

	var shadowEndpointDigest []byte
	if err := store.Pool.QueryRow(ctx, `SELECT media_manager_newznab_endpoint_config_sha256(config,secret_ref) FROM integration_instances WHERE id=$1`, shadowIndexerID.String()).Scan(&shadowEndpointDigest); err != nil {
		t.Fatal(err)
	}
	fixtureDocument, fixtureDocumentDigest, capsPlanDocument, capsPlanDocumentDigest, capsPlan := commissioningMigrationCAPSPlan(t, endpointDigest)
	for name, contract := range map[string][2]any{
		"version without key": {nil, int16(1)},
		"key without version": {"nzbfinder", nil},
	} {
		if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_commissioning_runs(
			id,indexer_instance_id,state,revision,profile_schema_version,planner_version,
			endpoint_config_sha256,provider_contract_key,provider_contract_version,
			fixture_document,fixture_sha256,caps_plan_document,caps_plan_sha256,
			maximum_api_calls,planned_api_calls,created_at,updated_at)
			VALUES($1,$2,'awaiting_caps_authorization',1,1,1,$3,$4,$5,$6,$7,$8,$9,5,1,$10,$10)`,
			uuid.NewV7().String(), indexerID.String(), endpointDigest, contract[0], contract[1], string(fixtureDocument),
			fixtureDocumentDigest, string(capsPlanDocument), capsPlanDocumentDigest, now); err == nil {
			t.Fatalf("database accepted provider-contract %s", name)
		}
	}
	runID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_commissioning_runs(
		id,indexer_instance_id,state,revision,profile_schema_version,planner_version,
		endpoint_config_sha256,fixture_document,fixture_sha256,caps_plan_document,
		caps_plan_sha256,maximum_api_calls,planned_api_calls,created_at,updated_at)
		VALUES($1,$2,'awaiting_caps_authorization',1,1,1,$3,$4,$5,$6,$7,5,1,$8,$8)`,
		runID.String(), indexerID.String(), endpointDigest, string(fixtureDocument), fixtureDocumentDigest,
		string(capsPlanDocument), capsPlanDocumentDigest, now); err != nil {
		t.Fatal(err)
	}
	capsStepID := uuid.NewV7()
	capsRequest, err := commissioningv2.CanonicalTypedRequest(capsPlan.Steps[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_commissioning_steps(
		id,run_id,ordinal,phase,target_kind,typed_request_document,structural_request_sha256,
		category_ids,request_limit,budget_family,state)
		VALUES($1,$2,1,'caps','caps',$3,$4,'{}',0,'query','planned')`,
		capsStepID.String(), runID.String(), string(capsRequest), capsPlan.Steps[0].StructuralSHA256.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET
		state='owner_attention',revision=2,safe_code='provider_token',completed_at=$2,updated_at=$2
		WHERE id=$1`, runID.String(), now.Add(time.Second)); err == nil {
		t.Fatal("database accepted arbitrary regex-safe run code")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET
		state='stale',revision=2,safe_code='credential.echo',stale_at=$2,completed_at=$2,updated_at=$2
		WHERE id=$1`, runID.String(), now.Add(time.Second)); err == nil {
		t.Fatal("database accepted a run code for the wrong state")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET
		state='caps_authorized',revision=2,caps_authorized_at=updated_at,caps_authorized_by='owner'
		WHERE id=$1`, runID.String()); err == nil {
		t.Fatal("database accepted an equal-time run mutation")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_steps SET
		state='intent_committed',intent_at=$2,executor_lease_owner='worker.unauthorized',executor_lease_until=$3
		WHERE id=$1`, capsStepID.String(), now.Add(time.Second), now.Add(time.Minute)); err == nil {
		t.Fatal("database started a commissioning step before phase authorization")
	}

	duplicateRunID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_commissioning_runs(
		id,indexer_instance_id,state,revision,profile_schema_version,planner_version,
		endpoint_config_sha256,fixture_document,fixture_sha256,caps_plan_document,
		caps_plan_sha256,maximum_api_calls,planned_api_calls,created_at,updated_at)
		VALUES($1,$2,'awaiting_caps_authorization',1,1,1,$3,$4,$5,$6,$7,5,1,$8,$8)`,
		duplicateRunID.String(), indexerID.String(), endpointDigest, string(fixtureDocument), fixtureDocumentDigest,
		string(capsPlanDocument), capsPlanDocumentDigest, now); err == nil {
		t.Fatal("database allowed two nonterminal commissioning runs for one indexer")
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_commissioning_runs(
		id,indexer_instance_id,state,revision,profile_schema_version,planner_version,
		endpoint_config_sha256,fixture_document,fixture_sha256,caps_plan_document,
		caps_plan_sha256,maximum_api_calls,planned_api_calls,created_at,updated_at)
		VALUES($1,$2,'awaiting_caps_authorization',1,1,1,$3,$4,$5,$6,$7,5,1,$8,$8)`,
		uuid.NewV7().String(), otherID.String(), endpointDigest, string(fixtureDocument), fixtureDocumentDigest,
		string(capsPlanDocument), capsPlanDocumentDigest, now); err == nil {
		t.Fatal("database allowed a commissioning run for a non-Newznab integration")
	}

	t1 := now.Add(time.Second)
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET
		state='caps_authorized',revision=2,caps_authorized_at=$2,caps_authorized_by='owner',updated_at=$2
		WHERE id=$1`, runID.String(), t1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET state='awaiting_category_review',revision=3,updated_at=$2 WHERE id=$1`, runID.String(), t1.Add(time.Second)); err == nil {
		t.Fatal("database advanced without exact completed CAPS evidence")
	}
	tIntent, tTransmit, tComplete := t1.Add(time.Second), t1.Add(2*time.Second), t1.Add(3*time.Second)
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_steps SET state='intent_committed',intent_at=$2,executor_lease_owner='worker.test',executor_lease_until=$3 WHERE id=$1`, capsStepID.String(), tIntent, tComplete.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_steps SET state='transmitting',transmitted_at=$2 WHERE id=$1`, capsStepID.String(), tTransmit); err != nil {
		t.Fatal(err)
	}
	capsEvidence := commissioningRepositoryCAPSEvidence(tComplete)
	capsEvidenceDocument, err := commissioningv2.CanonicalStepEvidence(capsEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_steps SET
		state='completed',completed_at=$2,executor_lease_owner=NULL,executor_lease_until=NULL,
		safe_outcome='caps_observed',safe_response_sha256=$3,raw_body_sha256=$4,
		evidence_key=$5,evidence_document=$6 WHERE id=$1`, capsStepID.String(), tComplete,
		commissioningv2.SumSHA256([]byte("safe CAPS projection")).Bytes(), capsEvidence.BodySHA256.Bytes(),
		capsEvidence.Key, string(capsEvidenceDocument)); err == nil {
		t.Fatal("database accepted durable evidence without explicit result counts")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_steps SET
		state='completed',completed_at=$2,executor_lease_owner=NULL,executor_lease_until=NULL,
		safe_outcome='caps_observed',safe_code='owner.cancelled',raw_result_count=0,plausible_identity_count=0,
		safe_response_sha256=$3,raw_body_sha256=$4,evidence_key=$5,evidence_document=$6 WHERE id=$1`,
		capsStepID.String(), tComplete, commissioningv2.SumSHA256([]byte("safe CAPS projection")).Bytes(),
		capsEvidence.BodySHA256.Bytes(), capsEvidence.Key, string(capsEvidenceDocument)); err == nil {
		t.Fatal("database accepted a step code for the wrong outcome")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_steps SET
		state='completed',completed_at=$2,executor_lease_owner=NULL,executor_lease_until=NULL,
		safe_outcome='caps_observed',raw_result_count=0,plausible_identity_count=0,
		safe_response_sha256=$3,raw_body_sha256=$4,evidence_key=$5,evidence_document=$6
		WHERE id=$1`, capsStepID.String(), tComplete, commissioningv2.SumSHA256([]byte("safe CAPS projection")).Bytes(),
		capsEvidence.BodySHA256.Bytes(), capsEvidence.Key, string(capsEvidenceDocument)); err != nil {
		t.Fatal(err)
	}
	t2 := tComplete.Add(time.Second)
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET state='awaiting_category_review',revision=3,updated_at=$2 WHERE id=$1`, runID.String(), t2); err != nil {
		t.Fatal(err)
	}
	configurationDigest, err := commissioningv2.SHA256DigestFromBytes(endpointDigest)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := commissioningRepositoryFixtures()
	categories := newznab.NewznabCategorySelection{Movie: []int{2000}, Series: []int{5000}}
	searchPlan, err := commissioningv2.BuildSearchPlan(commissioningv2.SearchPlanInput{
		ConfigurationSHA256: configurationDigest,
		Fixtures:            fixtures,
		CAPSEvidence:        capsEvidence,
		Categories:          categories,
		Options: commissioningv2.SearchProbeOptions{
			IncludeMovieFallback:          true,
			IncludeCompleteSeasonFallback: true,
		},
		ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	searchPlanDocument, err := commissioningv2.CanonicalProbePlan(searchPlan)
	if err != nil {
		t.Fatal(err)
	}
	_, categoryDigest, err := commissioningv2.NormalizeCategorySelection(categories)
	if err != nil {
		t.Fatal(err)
	}
	t3 := t2.Add(time.Second)
	for name, categoryArrays := range map[string][2]string{
		"movie null":  {"{2000,NULL}", "{5000}"},
		"series null": {"{2000}", "{5000,NULL}"},
	} {
		if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET
			state='awaiting_search_authorization',revision=4,
			category_selection_sha256=$2,movie_category_ids=$3::integer[],series_category_ids=$4::integer[],
			search_plan_document=$5,search_plan_sha256=$6,planned_api_calls=$7,updated_at=$8 WHERE id=$1`,
			runID.String(), categoryDigest.Bytes(), categoryArrays[0], categoryArrays[1], string(searchPlanDocument),
			commissioningv2.SumSHA256(searchPlanDocument).Bytes(), int16(searchPlan.PlannedAPICalls), t3); err == nil {
			t.Fatalf("database accepted %s category element", name)
		}
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET
		state='awaiting_search_authorization',revision=4,
		category_selection_sha256=$2,movie_category_ids=$3,series_category_ids=$4,
		search_plan_document=$5,search_plan_sha256=$6,
		planned_api_calls=$7,updated_at=$8 WHERE id=$1`, runID.String(), categoryDigest.Bytes(), categories.Movie,
		categories.Series, string(searchPlanDocument), commissioningv2.SumSHA256(searchPlanDocument).Bytes(),
		int16(searchPlan.PlannedAPICalls), t3); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET
		state='searches_authorized',revision=5,search_authorized_at=$2,search_authorized_by='owner',updated_at=$2
		WHERE id=$1`, runID.String(), t3.Add(time.Second)); err == nil {
		t.Fatal("database authorized searches before the sealed steps existed")
	}
	for stepIndex, planned := range searchPlan.Steps {
		stepID := uuid.NewV7()
		requestDocument, requestErr := commissioningv2.CanonicalTypedRequest(planned.Request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if stepIndex == 0 {
			if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_commissioning_steps(
				id,run_id,ordinal,phase,target_kind,recipe_key,scope,role,fixture_key,fixture_sha256,
				typed_request_document,structural_request_sha256,category_ids,request_limit,budget_family,state)
				VALUES($1,$2,$3,'search','search_recipe',NULL,$4,$5,$6,$7,$8,$9,$10,$11,'query','planned')`,
				uuid.NewV7().String(), runID.String(), int16(planned.Ordinal), string(planned.Scope), string(planned.Role),
				planned.FixtureKey, planned.FixtureSHA256.Bytes(), string(requestDocument), planned.StructuralSHA256.Bytes(),
				planned.CategoryIDs, int16(planned.Request.RequestLimit)); err == nil {
				t.Fatal("database accepted a search target without an exact recipe key")
			}
			for name, invalidCategories := range map[string]string{"duplicate": "{2000,2000}", "null": "{2000,NULL}"} {
				if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_commissioning_steps(
					id,run_id,ordinal,phase,target_kind,recipe_key,scope,role,fixture_key,fixture_sha256,
					typed_request_document,structural_request_sha256,category_ids,request_limit,budget_family,state)
					VALUES($1,$2,$3,'search','search_recipe',$4,$5,$6,$7,$8,$9,$10,$11::integer[],$12,'query','planned')`,
					uuid.NewV7().String(), runID.String(), int16(planned.Ordinal), planned.Target.RecipeKey, string(planned.Scope),
					string(planned.Role), planned.FixtureKey, planned.FixtureSHA256.Bytes(), string(requestDocument),
					planned.StructuralSHA256.Bytes(), invalidCategories, int16(planned.Request.RequestLimit)); err == nil {
					t.Fatalf("database accepted %s commissioning step categories", name)
				}
			}
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_commissioning_steps(
			id,run_id,ordinal,phase,target_kind,recipe_key,scope,role,fixture_key,fixture_sha256,
			typed_request_document,structural_request_sha256,category_ids,request_limit,budget_family,state)
			VALUES($1,$2,$3,'search','search_recipe',$4,$5,$6,$7,$8,$9,$10,$11,$12,'query','planned')`,
			stepID.String(), runID.String(), int16(planned.Ordinal), planned.Target.RecipeKey, string(planned.Scope),
			string(planned.Role), planned.FixtureKey, planned.FixtureSHA256.Bytes(), string(requestDocument),
			planned.StructuralSHA256.Bytes(), planned.CategoryIDs, int16(planned.Request.RequestLimit)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET
		state='searches_authorized',revision=5,search_authorized_at=$2,search_authorized_by='owner',updated_at=$2
		WHERE id=$1`, runID.String(), t3.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	t4 := t3.Add(time.Second)
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET state='ready_for_draft',revision=6,updated_at=$2 WHERE id=$1`, runID.String(), t4.Add(time.Second)); err == nil {
		t.Fatal("database made a run draft-ready while search steps remained planned")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_commissioning_runs SET fixture_document='{}'::jsonb,revision=6,updated_at=$2 WHERE id=$1`, runID.String(), t4.Add(time.Second)); err == nil {
		t.Fatal("database allowed immutable fixture evidence to change")
	}
	if _, err := store.Pool.Exec(ctx, `DELETE FROM indexer_commissioning_runs WHERE id=$1`, runID.String()); err == nil {
		t.Fatal("database allowed commissioning run history deletion")
	}
	if _, err := store.Pool.Exec(ctx, `DELETE FROM indexer_commissioning_steps WHERE id=$1`, capsStepID.String()); err == nil {
		t.Fatal("database allowed commissioning step history deletion")
	}

	connection, err := store.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	shadowFixtureDocument, shadowFixtureDigest, shadowCAPSPlanDocument, shadowCAPSPlanDigest, shadowCAPSPlan := commissioningMigrationCAPSPlan(t, shadowEndpointDigest)
	var shadowRunID uuid.UUID
	insertShadowRun := func(digest []byte) error {
		shadowRunID = uuid.NewV7()
		_, err := connection.Exec(ctx, `INSERT INTO `+schema+`.indexer_commissioning_runs(
			id,indexer_instance_id,state,revision,profile_schema_version,planner_version,
			endpoint_config_sha256,fixture_document,fixture_sha256,caps_plan_document,
			caps_plan_sha256,maximum_api_calls,planned_api_calls,created_at,updated_at)
			VALUES($1,$2,'awaiting_caps_authorization',1,1,1,$3,$4,$5,$6,$7,5,1,$8,$8)`,
			shadowRunID.String(), shadowIndexerID.String(), digest, string(shadowFixtureDocument), shadowFixtureDigest,
			string(shadowCAPSPlanDocument), shadowCAPSPlanDigest, t4.Add(2*time.Second))
		return err
	}
	if err := insertShadowRun(make([]byte, 32)); err == nil {
		t.Fatal("invalid commissioning digest passed before temporary shadows")
	}
	if _, err := connection.Exec(ctx, `
		CREATE TEMP TABLE shadow_bootstrap(value integer);
		CREATE DOMAIN pg_temp.text AS pg_catalog.int4;
		CREATE DOMAIN pg_temp.int4 AS pg_catalog.text;
		CREATE DOMAIN pg_temp.jsonb AS pg_catalog.text;
		CREATE DOMAIN pg_temp.bytea AS pg_catalog.text;
		CREATE DOMAIN pg_temp.inet AS pg_catalog.text;
		CREATE TEMP TABLE indexer_commissioning_runs(planned_api_calls integer);
		CREATE TEMP TABLE indexer_commissioning_steps(state text);
		CREATE FUNCTION pg_temp.media_manager_newznab_endpoint_config_sha256(pg_catalog.jsonb,pg_catalog.text)
		RETURNS pg_catalog.bytea LANGUAGE sql IMMUTABLE AS 'SELECT decode(repeat(''00'',32),''hex'')'
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `SET search_path TO pg_temp, `+schema); err != nil {
		t.Fatal(err)
	}
	if err := insertShadowRun(make([]byte, 32)); err == nil {
		t.Fatal("temporary helper or type shadow bypassed commissioning digest validation")
	}
	if err := insertShadowRun(shadowEndpointDigest); err != nil {
		t.Fatalf("schema-stable commissioning trigger rejected valid run under temporary shadows: %v", err)
	}
	shadowCAPSRequest, err := commissioningv2.CanonicalTypedRequest(shadowCAPSPlan.Steps[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `INSERT INTO `+schema+`.indexer_commissioning_steps(
		id,run_id,ordinal,phase,target_kind,typed_request_document,structural_request_sha256,
		category_ids,request_limit,budget_family,state)
		VALUES($1,$2,1,'caps','caps',$3,$4,'{}',0,'query','planned')`, uuid.NewV7().String(), shadowRunID.String(),
		string(shadowCAPSRequest), shadowCAPSPlan.Steps[0].StructuralSHA256.Bytes()); err != nil {
		t.Fatalf("schema-stable step trigger rejected a valid plan under temporary shadows: %v", err)
	}
	if _, err := connection.Exec(ctx, `UPDATE `+schema+`.indexer_commissioning_runs SET
		state='caps_authorized',revision=2,caps_authorized_at=$2,caps_authorized_by='owner',updated_at=$2
		WHERE id=$1`, shadowRunID.String(), t4.Add(3*time.Second)); err != nil {
		t.Fatalf("schema-stable run trigger could not resolve its real steps under temporary shadows: %v", err)
	}

	if err := migrateThrough(ctx, store.Pool, 34); err != nil {
		t.Fatalf("repeated migration: %v", err)
	}
}

type commissioningSchema33SnapshotValue struct {
	integrationCount       int
	newznabCount           int
	enabledCount           int
	profileCount           int
	activeProfileCount     int
	operationStatusCount   int
	auditEventCount        int
	integrationStateSHA256 []byte
	profileStateSHA256     []byte
}

func commissioningSchema33State(t *testing.T, ctx context.Context, store *Store) commissioningSchema33SnapshotValue {
	t.Helper()
	var result commissioningSchema33SnapshotValue
	err := store.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM integration_instances),
		(SELECT count(*) FROM integration_instances WHERE kind='newznab'),
		(SELECT count(*) FROM integration_instances WHERE enabled),
		(SELECT count(*) FROM indexer_capability_profiles),
		(SELECT count(*) FROM indexer_capability_profiles WHERE lifecycle_state='active'),
		(SELECT count(*) FROM indexer_operation_status),
		(SELECT count(*) FROM audit_events),
		(SELECT sha256(convert_to(COALESCE(jsonb_agg(to_jsonb(integration) ORDER BY integration.id),'[]'::jsonb)::text,'UTF8')) FROM integration_instances integration),
		(SELECT sha256(convert_to(COALESCE(jsonb_agg(to_jsonb(profile) ORDER BY profile.id),'[]'::jsonb)::text,'UTF8')) FROM indexer_capability_profiles profile)`).Scan(
		&result.integrationCount,
		&result.newznabCount,
		&result.enabledCount,
		&result.profileCount,
		&result.activeProfileCount,
		&result.operationStatusCount,
		&result.auditEventCount,
		&result.integrationStateSHA256,
		&result.profileStateSHA256,
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func commissioningMigrationCAPSPlan(t *testing.T, endpointDigest []byte) ([]byte, []byte, []byte, []byte, commissioningv2.ProbePlan) {
	t.Helper()
	digest, err := commissioningv2.SHA256DigestFromBytes(endpointDigest)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := commissioningRepositoryFixtures()
	fixtureDocument, err := commissioningv2.CanonicalFixtureDocument(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{
		ConfigurationSHA256:  digest,
		Fixtures:             fixtures,
		ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	planDocument, err := commissioningv2.CanonicalProbePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	return fixtureDocument, commissioningv2.SumSHA256(fixtureDocument).Bytes(), planDocument, commissioningv2.SumSHA256(planDocument).Bytes(), plan
}
