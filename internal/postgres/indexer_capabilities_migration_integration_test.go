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
	missingID, nullID, emptyID := uuid.NewV7(), uuid.NewV7(), uuid.NewV7()
	unsafeEndpointID, unsafeReferenceID := uuid.NewV7(), uuid.NewV7()
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
	unsafeLegacyEndpoints := []struct {
		name      string
		config    string
		secretRef string
	}{
		{name: "Unsafe query endpoint", config: `{"base_url":"https://unsafe-query.invalid/api?x=y"}`, secretRef: "UNSAFE_QUERY_KEY"},
		{name: "Unsafe fragment endpoint", config: `{"base_url":"https://unsafe-fragment.invalid/api#part"}`, secretRef: "UNSAFE_FRAGMENT_KEY"},
		{name: "Unsafe percent endpoint", config: `{"base_url":"https://unsafe-percent.invalid/%zz"}`, secretRef: "UNSAFE_PERCENT_KEY"},
		{name: "Unsafe backslash endpoint", config: `{"base_url":"https://unsafe-backslash.invalid/a\\b"}`, secretRef: "UNSAFE_BACKSLASH_KEY"},
		{name: "Unsafe whitespace endpoint", config: `{"base_url":" https://unsafe-whitespace.invalid/api"}`, secretRef: "UNSAFE_WHITESPACE_KEY"},
		{name: "Unsafe control endpoint", config: `{"base_url":"https://unsafe-control.invalid/a\u0001b"}`, secretRef: "UNSAFE_CONTROL_KEY"},
		{name: "Unsafe HTTP endpoint", config: `{"base_url":"http://unsafe-http.invalid/api"}`, secretRef: "UNSAFE_HTTP_KEY"},
		{name: "Unsafe missing authority", config: `{"base_url":"https:///api"}`, secretRef: "UNSAFE_AUTHORITY_KEY"},
	}
	unsafeBindingIDs := []string{unsafeEndpointID.String(), unsafeReferenceID.String()}
	for _, fixture := range unsafeLegacyEndpoints {
		id := uuid.NewV7()
		unsafeBindingIDs = append(unsafeBindingIDs, id.String())
		if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab',$2,false,false,$3,$4,$5,$5)`, id.String(), fixture.name, fixture.config, fixture.secretRef, now); err != nil {
			t.Fatal(err)
		}
	}
	missingReferenceID := uuid.NewV7()
	unsafeBindingIDs = append(unsafeBindingIDs, missingReferenceID.String())
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab','Missing secret reference',false,false,$2,NULL,$3,$3)`, missingReferenceID.String(), `{"base_url":"https://missing-reference.invalid/api"}`, now); err != nil {
		t.Fatal(err)
	}
	invalidConfigID := uuid.NewV7()
	unsafeBindingIDs = append(unsafeBindingIDs, invalidConfigID.String())
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab','Invalid scalar configuration',false,false,'null'::jsonb,'INVALID_CONFIG_KEY',$2,$2)`, invalidConfigID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES
		($1,'newznab','Missing legacy categories',false,false,$6,'MISSING_KEY',$11,$11),
		($2,'newznab','Null legacy categories',false,false,$7,'NULL_KEY',$11,$11),
		($3,'newznab','Empty legacy categories',false,false,$8,'EMPTY_KEY',$11,$11),
		($4,'newznab','Unsafe legacy endpoint',false,false,$9,'UNSAFE_ENDPOINT_KEY',$11,$11),
		($5,'newznab','Unsafe secret reference',false,false,$10,'not a reference',$11,$11)`,
		missingID.String(), nullID.String(), emptyID.String(), unsafeEndpointID.String(), unsafeReferenceID.String(),
		`{"base_url":"https://missing.invalid/api"}`,
		`{"base_url":"https://null.invalid/api","categories":null}`,
		`{"base_url":"https://empty.invalid/api","categories":[]}`,
		`{"base_url":"https://user@unsafe.invalid/api"}`,
		`{"base_url":"https://unsafe-reference.invalid/api","categories":[2000]}`,
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
	var equivalentEndpointDigest, canonicalPathDigest, caseSensitiveReferenceDigest, distinctReferenceDigest, allowedHTTPDigest, unsafeBindingRejected bool
	if err := store.Pool.QueryRow(ctx, `SELECT
		media_manager_newznab_endpoint_config_sha256('{"base_url":"https://INDEXER.invalid:0443/api"}'::jsonb,'INDEXER_KEY')
			= media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api"}'::jsonb,'INDEXER_KEY'),
		media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api"}'::jsonb,'INDEXER_KEY')
			= media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api/"}'::jsonb,'INDEXER_KEY'),
		media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api"}'::jsonb,'INDEXER_KEY')
			<> media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api"}'::jsonb,'indexer_key'),
		media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api"}'::jsonb,'INDEXER_KEY')
			<> media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api"}'::jsonb,'OTHER_INDEXER_KEY'),
		media_manager_newznab_endpoint_config_sha256('{"base_url":"http://indexer.invalid/api","allow_http":true}'::jsonb,'INDEXER_KEY') IS NOT NULL,
		media_manager_newznab_endpoint_config_sha256('{"base_url":" https://indexer.invalid/api"}'::jsonb,'INDEXER_KEY') IS NULL
			AND media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/%zz"}'::jsonb,'INDEXER_KEY') IS NULL
			AND media_manager_newznab_endpoint_config_sha256(jsonb_build_object('base_url','https://indexer.invalid/a'||chr(92)||'b'),'INDEXER_KEY') IS NULL
			AND media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api?x=y"}'::jsonb,'INDEXER_KEY') IS NULL
			AND media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api#fragment"}'::jsonb,'INDEXER_KEY') IS NULL
			AND media_manager_newznab_endpoint_config_sha256('{"base_url":"http://indexer.invalid/api"}'::jsonb,'INDEXER_KEY') IS NULL
			AND media_manager_newznab_endpoint_config_sha256('{"base_url":"https://indexer.invalid/api"}'::jsonb,'not a reference') IS NULL`).Scan(
		&equivalentEndpointDigest, &canonicalPathDigest, &caseSensitiveReferenceDigest, &distinctReferenceDigest, &allowedHTTPDigest, &unsafeBindingRejected); err != nil {
		t.Fatal(err)
	}
	if !equivalentEndpointDigest || !canonicalPathDigest || !caseSensitiveReferenceDigest || !distinctReferenceDigest || !allowedHTTPDigest || !unsafeBindingRejected {
		t.Fatalf("endpoint configuration digest normalization: equivalent=%v canonical_path=%v case_sensitive_ref=%v distinct_ref=%v allowed_http=%v unsafe_rejected=%v", equivalentEndpointDigest, canonicalPathDigest, caseSensitiveReferenceDigest, distinctReferenceDigest, allowedHTTPDigest, unsafeBindingRejected)
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
	assertConfig(missingID, []int{}, []int{}, true)
	assertConfig(nullID, []int{}, []int{}, true)
	assertConfig(emptyID, []int{}, []int{}, true)
	assertConfig(unsafeEndpointID, []int{}, []int{}, true)
	assertConfig(unsafeReferenceID, []int{2000}, []int{2000}, true)

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
	var invalidConfigUnchanged bool
	if err := store.Pool.QueryRow(ctx, `SELECT config='null'::jsonb FROM integration_instances WHERE id=$1`, invalidConfigID.String()).Scan(&invalidConfigUnchanged); err != nil || !invalidConfigUnchanged {
		t.Fatalf("invalid legacy configuration was rewritten: unchanged=%v err=%v", invalidConfigUnchanged, err)
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
	if profiles != 6 || activeProfiles != 0 || operationStatuses != 0 || untruthfulProfiles != 0 {
		t.Fatalf("profiles=%d active=%d statuses=%d untruthful=%d", profiles, activeProfiles, operationStatuses, untruthfulProfiles)
	}
	var unsafeProfiles int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_capability_profiles WHERE indexer_instance_id=ANY($1::uuid[])`, unsafeBindingIDs).Scan(&unsafeProfiles); err != nil || unsafeProfiles != 0 {
		t.Fatalf("unsafe legacy bindings created drafts=%d err=%v", unsafeProfiles, err)
	}
	for _, unsafeID := range unsafeBindingIDs {
		parsedID, parseErr := uuid.Parse(unsafeID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if _, err := store.IndexerEndpointConfigurationDigest(ctx, parsedID); !errors.Is(err, ErrInvalidIndexerCapabilityProfile) {
			t.Fatalf("unsafe legacy integration %s produced a repository binding: %v", parsedID, err)
		}
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
