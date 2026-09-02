package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"uuid"
)

func TestIndexerCapabilityMigrationIsAdditiveAndTruthful(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	schema := "mm_indexer_capability_migration_" + strings.ReplaceAll(uuid.New().String(), "-", "")
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
	if err := migrateThrough(ctx, store.Pool, 32); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	legacyID, splitID, malformedID, otherID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES
		($1,'newznab','Legacy categories',true,false,$5,'LEGACY_KEY',$9,$9),
		($2,'newznab','Split categories',false,false,$6,'SPLIT_KEY',$9,$9),
		($3,'newznab','Malformed legacy categories',false,false,$7,'MALFORMED_KEY',$9,$9),
		($4,'altmount','Unrelated integration',true,false,$8,NULL,$9,$9)`,
		legacyID.String(), splitID.String(), malformedID.String(), otherID.String(),
		`{"base_url":"https://legacy.invalid/api","mode_verified":true,"categories":[2000,2040],"minimum_interval_ms":4000,"query_limit":25,"grab_limit":5,"budget_window_seconds":3600,"search_dialects":{"movie":{"mode":"movie","params":["q","imdbid"]}}}`,
		`{"base_url":"https://split.invalid/api","mode_verified":false,"categories":[9999],"movie_category_ids":[2010],"series_category_ids":[5030]}`,
		`{"base_url":"https://malformed.invalid/api","mode_verified":false,"categories":{"unexpected":true}}`,
		`{"categories":[42],"unchanged":true}`,
		now); err != nil {
		t.Fatal(err)
	}

	if err := migrateThrough(ctx, store.Pool, 33); err != nil {
		t.Fatal(err)
	}
	var schemaVersion int64
	if err := store.Pool.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != 33 {
		t.Fatalf("schema=%d want 33", schemaVersion)
	}
	var equivalentEndpointDigest, distinctPathDigest bool
	if err := store.Pool.QueryRow(ctx, `SELECT
		media_manager_newznab_endpoint_config_sha256('{"base_url":"https://INDEXER.invalid:0443/api"}'::jsonb)
			= media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api"}'::jsonb),
		media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api"}'::jsonb)
			<> media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api/"}'::jsonb)`).Scan(&equivalentEndpointDigest, &distinctPathDigest); err != nil {
		t.Fatal(err)
	}
	if !equivalentEndpointDigest || !distinctPathDigest {
		t.Fatalf("endpoint configuration digest normalization: equivalent=%v distinct_path=%v", equivalentEndpointDigest, distinctPathDigest)
	}

	assertConfig := func(id uuid.UUID, movie, series []int, reviewRequired bool) {
		t.Helper()
		var body []byte
		if err := store.Pool.QueryRow(ctx, `SELECT config FROM integration_instances WHERE id=$1`, id.String()).Scan(&body); err != nil {
			t.Fatal(err)
		}
		var config struct {
			Movie          []int `json:"movie_category_ids"`
			Series         []int `json:"series_category_ids"`
			ReviewRequired bool  `json:"category_selection_review_required"`
		}
		if err := json.Unmarshal(body, &config); err != nil {
			t.Fatal(err)
		}
		if !equalIntSet(config.Movie, movie) || !equalIntSet(config.Series, series) || config.ReviewRequired != reviewRequired {
			t.Fatalf("config=%s", body)
		}
	}
	assertConfig(legacyID, []int{2000, 2040}, []int{2000, 2040}, true)
	assertConfig(splitID, []int{2010}, []int{5030}, true)
	assertConfig(malformedID, []int{}, []int{}, true)

	var legacyEnabled bool
	var legacySecret string
	var legacyConfig, otherConfig []byte
	if err := store.Pool.QueryRow(ctx, `SELECT enabled,secret_ref,config FROM integration_instances WHERE id=$1`, legacyID.String()).Scan(&legacyEnabled, &legacySecret, &legacyConfig); err != nil {
		t.Fatal(err)
	}
	if !legacyEnabled || legacySecret != "LEGACY_KEY" || !strings.Contains(string(legacyConfig), `"mode_verified": true`) || !strings.Contains(string(legacyConfig), `"minimum_interval_ms": 4000`) || !strings.Contains(string(legacyConfig), `"search_dialects"`) {
		t.Fatalf("legacy integration changed unexpectedly: enabled=%v secret=%q config=%s", legacyEnabled, legacySecret, legacyConfig)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT config FROM integration_instances WHERE id=$1`, otherID.String()).Scan(&otherConfig); err != nil {
		t.Fatal(err)
	}
	if string(otherConfig) != `{"unchanged": true, "categories": [42]}` {
		t.Fatalf("unrelated integration changed: %s", otherConfig)
	}

	var profiles, activeProfiles, operationStatuses, untruthfulProfiles int
	if err := store.Pool.QueryRow(ctx, `SELECT
		count(*),
		count(*) FILTER (WHERE lifecycle_state='active'),
		(SELECT count(*) FROM indexer_operation_status),
		count(*) FILTER (WHERE profile_document::text LIKE '%verified%')
		FROM indexer_capability_profiles`).Scan(&profiles, &activeProfiles, &operationStatuses, &untruthfulProfiles); err != nil {
		t.Fatal(err)
	}
	if profiles != 3 || activeProfiles != 0 || operationStatuses != 0 || untruthfulProfiles != 0 {
		t.Fatalf("profiles=%d active=%d statuses=%d untruthful=%d", profiles, activeProfiles, operationStatuses, untruthfulProfiles)
	}
	var legacyProfileText string
	var revision int64
	var state string
	var digestLength, endpointDigestLength int
	if err := store.Pool.QueryRow(ctx, `SELECT id::text,revision,lifecycle_state,octet_length(document_sha256),octet_length(endpoint_config_sha256) FROM indexer_capability_profiles WHERE indexer_instance_id=$1`, legacyID.String()).Scan(&legacyProfileText, &revision, &state, &digestLength, &endpointDigestLength); err != nil {
		t.Fatal(err)
	}
	legacyProfileID, _ := uuid.Parse(legacyProfileText)
	if revision != 1 || state != "draft" || digestLength != 32 || endpointDigestLength != 32 || legacyProfileID == legacyID {
		t.Fatalf("legacy draft id=%s revision=%d state=%q digest=%d endpoint_digest=%d", legacyProfileID, revision, state, digestLength, endpointDigestLength)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_capability_profiles(id,indexer_instance_id,revision,schema_version,lifecycle_state,profile_document,document_sha256,endpoint_config_sha256,created_at,activated_at)
		SELECT uuidv7(),indexer_instance_id,99,schema_version,'active',profile_document,document_sha256,endpoint_config_sha256,now(),now()
		FROM indexer_capability_profiles WHERE id=$1`, legacyProfileID.String()); err == nil {
		t.Fatal("database allowed a capability profile to bypass draft activation")
	}
	loaded, err := store.IndexerCapabilityProfile(ctx, legacyProfileID)
	if err != nil || loaded.Revision != 1 || loaded.LifecycleState != "draft" {
		t.Fatalf("load migrated profile=%#v err=%v", loaded, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_capability_profiles SET lifecycle_state='active',activated_at=now() WHERE id=$1`, legacyProfileID.String()); err == nil {
		t.Fatal("database activated a review-required legacy draft directly")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{category_selection_review_required}','false'::jsonb,true) WHERE id=$1`, legacyID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, legacyID, legacyProfileID, "owner", time.Now().UTC()); !errors.Is(err, ErrInvalidIndexerCapabilityProfile) {
		t.Fatalf("legacy untested draft became active: %v", err)
	}

	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_capability_profiles SET profile_document='{}'::jsonb WHERE indexer_instance_id=$1`, legacyID.String()); err == nil {
		t.Fatal("database allowed an immutable profile document to change")
	}
	if _, err := store.Pool.Exec(ctx, `DELETE FROM indexer_capability_profiles WHERE indexer_instance_id=$1`, legacyID.String()); err == nil {
		t.Fatal("database allowed profile history to be deleted")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_capability_profiles SET endpoint_config_sha256=$2 WHERE indexer_instance_id=$1`, legacyID.String(), make([]byte, 32)); err == nil {
		t.Fatal("database allowed an immutable endpoint configuration digest to change")
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_capability_profiles SET created_at=created_at+interval '1 second' WHERE indexer_instance_id=$1`, legacyID.String()); err == nil {
		t.Fatal("database allowed immutable profile creation evidence to change")
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_capability_profiles(id,indexer_instance_id,revision,schema_version,lifecycle_state,profile_document,document_sha256,endpoint_config_sha256,created_at) SELECT uuidv7(),indexer_instance_id,2,1,'draft',profile_document,$2,endpoint_config_sha256,now() FROM indexer_capability_profiles WHERE indexer_instance_id=$1`, legacyID.String(), make([]byte, 32)); err == nil {
		t.Fatal("database accepted a profile with a mismatched document digest")
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_operation_status(indexer_instance_id,capability_profile_id,operation_key,recipe_key,availability,updated_at) VALUES($1,$2,'search','movie-primary','unknown',now())`, splitID.String(), legacyProfileID.String()); err == nil {
		t.Fatal("database accepted an operation status for another indexer's profile")
	}

	if err := migrateThrough(ctx, store.Pool, 33); err != nil {
		t.Fatalf("repeated migration: %v", err)
	}
}

func equalIntSet(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
