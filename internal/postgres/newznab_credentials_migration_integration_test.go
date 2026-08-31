package postgres

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"uuid"
)

func TestNewznabCredentialMigrationPreservesObservationsAndIsIdempotent(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	schema := "mm_credential_migration_" + strings.ReplaceAll(uuid.New().String(), "-", "")
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
	if err := migrateThrough(ctx, store.Pool, 26); err != nil {
		t.Fatal(err)
	}

	indexerID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab','Migration fixture',false,false,'{}','NEWZNAB_MIGRATION_KEY',now(),now())`, indexerID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at)
		SELECT (substr(md5(item::text),1,8)||'-'||substr(md5(item::text),9,4)||'-4'||substr(md5(item::text),14,3)||'-8'||substr(md5(item::text),18,3)||'-'||substr(md5(item::text),21,12))::uuid,
		$1,'migration-fixture','fixture-'||item,'Fixture.Release.'||item,decode(md5(item::text),'hex'),
		CASE item
			WHEN 1 THEN jsonb_build_array(jsonb_build_object('name','info','value','https://one.test/api?apikey=direct-secret&ticket=one','ordinal',0))
			WHEN 2 THEN jsonb_build_array(jsonb_build_object('name','info','value','https%3A%2F%2Ftwo.test%2Fapi%3FAPIKEY%3Dencoded-secret%26ticket%3Dtwo','ordinal',0))
			WHEN 3 THEN jsonb_build_array(jsonb_build_object('name','info','value','https://outer.test/get?redirect=https%3A%2F%2Finner.test%2Ffile%3Ftoken%3Dnested-secret%26ticket%3Dinner&ticket=outer','ordinal',0))
			WHEN 4 THEN jsonb_build_array(jsonb_build_object('name','info','value','https://four.test/api?ApiKey=first-secret&ticket=four&API_KEY=second-secret&keep=yes','ordinal',0))
			WHEN 5 THEN jsonb_build_array(jsonb_build_object('name','info','value','https://five.test/api?%61pikey=%zz-malformed-secret&ticket=five','ordinal',0))
			WHEN 6 THEN jsonb_build_array(jsonb_build_object('name','info','value','https://six.test/api?password=first-field-secret&ticket=six','ordinal',0),jsonb_build_object('name','comments','value','https://six.test/comments?client_secret=second-field-secret&page=2','ordinal',1),jsonb_build_object('name','group','value','preserved-group','ordinal',2))
			WHEN 7 THEN jsonb_build_array(jsonb_build_object('name','info','value','https://drunkenslug.com/details?id=seven&r=drunkenslug-r-secret&ticket=seven','ordinal',0))
			WHEN 8 THEN jsonb_build_array(jsonb_build_object('name','info','value','https://other.test/details?id=eight&r=benign-r-metadata&ticket=eight','ordinal',0))
			ELSE jsonb_build_array(jsonb_build_object('name','info','value','https://benign.test/details?ticket='||item||'&offset=20','ordinal',0),jsonb_build_object('name','group','value','preserved-group','ordinal',1))
		END,
		jsonb_build_array(jsonb_build_object('name','group','values',jsonb_build_array('preserved-group'),'state','observed','evidence_at',jsonb_build_array(0),'observed_at',now())),
		'newznab-v1','[]',now()
		FROM generate_series(1,10876) item`, indexerID.String()); err != nil {
		t.Fatal(err)
	}

	if err := migrateThrough(ctx, store.Pool, 27); err != nil {
		t.Fatal(err)
	}
	var before, affectedBefore, updated, after, affectedAfter int64
	if err := store.Pool.QueryRow(ctx, `SELECT observations_before,affected_rows_before,rows_updated,observations_after,affected_rows_after FROM security_migration_aggregates WHERE migration_version=27`).Scan(&before, &affectedBefore, &updated, &after, &affectedAfter); err != nil {
		t.Fatal(err)
	}
	if before != 10876 || after != 10876 || affectedBefore != 6 || updated != 6 || affectedAfter != 0 {
		t.Fatalf("migration aggregate before=%d affected_before=%d updated=%d after=%d affected_after=%d", before, affectedBefore, updated, after, affectedAfter)
	}
	if err := migrateThrough(ctx, store.Pool, 28); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT observations_before,affected_rows_before,rows_updated,observations_after,affected_rows_after FROM security_migration_aggregates WHERE migration_version=28`).Scan(&before, &affectedBefore, &updated, &after, &affectedAfter); err != nil {
		t.Fatal(err)
	}
	if before != 10876 || after != 10876 || affectedBefore != 1 || updated != 1 || affectedAfter != 0 {
		t.Fatalf("provider migration aggregate before=%d affected_before=%d updated=%d after=%d affected_after=%d", before, affectedBefore, updated, after, affectedAfter)
	}

	var exactMatches, patternMatches, preservedFacts int64
	secrets := []string{"direct-secret", "encoded-secret", "nested-secret", "first-secret", "second-secret", "malformed-secret", "first-field-secret", "second-field-secret", "drunkenslug-r-secret"}
	if err := store.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE raw_fields::text LIKE ANY($1)),
		count(*) FILTER (WHERE raw_fields::text ~* '([?&]|%3f|%26|%253f|%2526)(api[_%.-]*key|token|access[_%.-]*token|password|client[_%.-]*secret)(=|%3d|%253d)'),
		count(*) FILTER (WHERE normalized_facts::text LIKE '%preserved-group%')
		FROM release_observations`, likePatterns(secrets)).Scan(&exactMatches, &patternMatches, &preservedFacts); err != nil {
		t.Fatal(err)
	}
	if exactMatches != 0 || patternMatches != 0 || preservedFacts != 10876 {
		t.Fatalf("post-migration exact=%d patterns=%d preserved_facts=%d", exactMatches, patternMatches, preservedFacts)
	}
	rows, err := store.Pool.Query(ctx, `SELECT native_guid,raw_fields FROM release_observations WHERE native_guid IN ('fixture-1','fixture-2','fixture-3','fixture-4','fixture-5','fixture-6','fixture-7','fixture-8') ORDER BY native_guid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var native string
		var raw []byte
		if err := rows.Scan(&native, &raw); err != nil {
			t.Fatal(err)
		}
		if !json.Valid(raw) || !strings.Contains(string(raw), "ticket") || strings.Contains(string(raw), "secret") {
			t.Fatalf("sanitized evidence for %s did not preserve benign metadata", native)
		}
		seen++
	}
	if err := rows.Err(); err != nil || seen != 8 {
		t.Fatalf("sanitized fixture rows=%d err=%v", seen, err)
	}

	if err := migrateThrough(ctx, store.Pool, 28); err != nil {
		t.Fatal(err)
	}
	var repeatedUpdates int64
	if err := store.Pool.QueryRow(ctx, `WITH updated AS (
		UPDATE release_observations SET raw_fields=media_manager_sanitize_newznab_raw_fields(raw_fields)
		WHERE raw_fields IS DISTINCT FROM media_manager_sanitize_newznab_raw_fields(raw_fields) RETURNING 1
	) SELECT count(*) FROM updated`).Scan(&repeatedUpdates); err != nil || repeatedUpdates != 0 {
		t.Fatalf("idempotent update count=%d err=%v", repeatedUpdates, err)
	}

	triggerID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO release_observations(id,indexer_instance_id,operation_key,native_guid,title,body_sha256,raw_fields,normalized_facts,parse_version,problems,observed_at) VALUES($1,$2,'trigger','trigger-fixture','Trigger Fixture',$3,$4,'[]','newznab-v1','[]',now())`, triggerID.String(), indexerID.String(), []byte{1}, `[{
		"name":"info","value":"https://drunkenslug.com/details?r=trigger-secret&ticket=kept","ordinal":0
	}]`); err != nil {
		t.Fatal(err)
	}
	var triggerFields string
	if err := store.Pool.QueryRow(ctx, `SELECT raw_fields::text FROM release_observations WHERE id=$1`, triggerID.String()).Scan(&triggerFields); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(triggerFields, "trigger-secret") || !strings.Contains(triggerFields, "ticket=kept") {
		t.Fatal("database ingestion trigger did not sanitize the raw fields")
	}
}

func likePatterns(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, "%"+value+"%")
	}
	return result
}
