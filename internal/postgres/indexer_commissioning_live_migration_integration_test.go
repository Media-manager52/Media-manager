package postgres

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"uuid"
)

// migrationTestPool creates a disposable per-test schema and migrates it
// through the approved migrateThrough mechanism to exactly the requested
// schema version. Tests requiring 34→35 semantics genuinely begin from
// schema 34. Env-only DSN, skip when unset, prod-looking rejection, random
// schema per test, cleanup limited to that schema, timeout, no fallback.
func migrationTestPool(t *testing.T, maximumVersion int64) (*pgxpool.Pool, string, context.Context) {
	t.Helper()
	databaseURL := strings.TrimSpace(envOrEmpty("MEDIA_MANAGER_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	lowered := strings.ToLower(databaseURL)
	if strings.Contains(lowered, "prod") && !strings.Contains(lowered, "test") {
		t.Fatalf("refusing to use a non-test database URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	schema := "mm_live_migration_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := pgxpool.New(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrateThrough(ctx, pool, maximumVersion); err != nil {
		t.Fatal(err)
	}
	return pool, schema, ctx
}

// migrationIndexer seeds one integration_instances row whose config digest
// the 0034 commissioning helpers can read.
func migrationIndexer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, now time.Time) uuid.UUID {
	t.Helper()
	id := uuid.NewV7()
	if _, err := pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab',$2,false,false,$3,'LIVE_MIGRATION_KEY',$4,$4)`, id.String(), "Live migration fixture "+id.String(), `{"base_url":"https://live-migration.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000],"category_selection_review_required":false}`, now); err != nil {
		t.Fatal(err)
	}
	return id
}

// A-1: migration 35 applies cleanly from 34.
func TestMigration35AppliesCleanlyFrom34(t *testing.T) {
	pool, schema, ctx := migrationTestPool(t, 34)
	version, err := SchemaVersion(ctx, pool)
	if err != nil || version != 34 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	if err := migrateThrough(ctx, pool, 35); err != nil {
		t.Fatal(err)
	}
	version, err = SchemaVersion(ctx, pool)
	if err != nil || version != 35 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var hasGate, hasOutcomeAt bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='indexer_budget_state' AND column_name='commissioning_gate_step_id'), EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='indexer_budget_state' AND column_name='last_outcome_at')`, schema, schema).Scan(&hasGate, &hasOutcomeAt); err != nil {
		t.Fatal(err)
	}
	if !hasGate || !hasOutcomeAt {
		t.Fatal("0035 columns missing")
	}
	var hasFK, hasCheck, hasIndex, hasTrigger, hasFunction bool
	if err := pool.QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM information_schema.table_constraints WHERE table_schema=$1 AND table_name='indexer_budget_state' AND constraint_name='indexer_budget_state_gate_step_fk'),
		EXISTS (SELECT 1 FROM information_schema.table_constraints WHERE table_schema=$1 AND table_name='indexer_budget_state' AND constraint_name='indexer_budget_state_outcome_time'),
		EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname=$1 AND tablename='indexer_budget_state' AND indexname='indexer_budget_state_one_gate_per_step'),
		EXISTS (SELECT 1 FROM information_schema.triggers WHERE trigger_schema=$1 AND event_object_table='indexer_budget_state' AND trigger_name='indexer_budget_state_commissioning_gate'),
		EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname='enforce_indexer_budget_commissioning_gate')`, schema).Scan(&hasFK, &hasCheck, &hasIndex, &hasTrigger, &hasFunction); err != nil {
		t.Fatal(err)
	}
	if !hasFK || !hasCheck || !hasIndex || !hasTrigger || !hasFunction {
		t.Fatalf("0035 objects missing: fk=%v check=%v index=%v trigger=%v function=%v", hasFK, hasCheck, hasIndex, hasTrigger, hasFunction)
	}
}

// A-2: seed at 34, snapshot all 7 columns, migrate, assert byte-equality and
// both new columns NULL.
func TestMigration35PreservesExistingBudgetRows(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 34)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := migrationIndexer(t, ctx, pool, now)
	var id string
	var minimumMS int64
	var next, cooldown, updated *time.Time
	var failures int32
	var outcome *string
	if err := pool.QueryRow(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,revision,updated_at) VALUES($1,5000,$2,$3,4,'success',7,$4) RETURNING indexer_instance_id::text,minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,revision,updated_at`, indexerID.String(), now, now.Add(time.Hour), now).Scan(&id, &minimumMS, &next, &cooldown, &failures, &outcome, new(*int64), &updated); err != nil {
		t.Fatal(err)
	}
	var bID string
	var bMin int64
	var bNext, bCooldown, bUpdated *time.Time
	var bFail int32
	var bOutcome *string
	var bRevision int64
	if err := pool.QueryRow(ctx, `SELECT indexer_instance_id::text,minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,revision,updated_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&bID, &bMin, &bNext, &bCooldown, &bFail, &bOutcome, &bRevision, &bUpdated); err != nil {
		t.Fatal(err)
	}
	if err := migrateThrough(ctx, pool, 35); err != nil {
		t.Fatal(err)
	}
	var aID string
	var aMin int64
	var aNext, aCooldown, aUpdated, aOutcomeAt *time.Time
	var aFail int32
	var aOutcome *string
	var aRevision int64
	var aGate *string
	if err := pool.QueryRow(ctx, `SELECT indexer_instance_id::text,minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,revision,updated_at,last_outcome_at,commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&aID, &aMin, &aNext, &aCooldown, &aFail, &aOutcome, &aRevision, &aUpdated, &aOutcomeAt, &aGate); err != nil {
		t.Fatal(err)
	}
	if aID != bID || aMin != bMin || !timesEqual(aNext, bNext) || !timesEqual(aCooldown, bCooldown) || aFail != bFail || !stringsEqual(aOutcome, bOutcome) || aRevision != bRevision || !timesEqual(aUpdated, bUpdated) {
		t.Fatalf("schema-34 columns changed by migration: before id=%s min=%d fail=%d rev=%d, after id=%s min=%d fail=%d rev=%d", bID, bMin, bFail, bRevision, aID, aMin, aFail, aRevision)
	}
	if aOutcomeAt != nil || aGate != nil {
		t.Fatalf("new columns not NULL after migration: outcomeAt=%v gate=%v", aOutcomeAt, aGate)
	}
}

func timesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func stringsEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// A-3: no historical backfill — last_outcome='success' keeps
// last_outcome_at IS NULL.
func TestMigration35DoesNotBackfillOutcomeTime(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 34)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := migrationIndexer(t, ctx, pool, now)
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,next_allowed_at,cooldown_until,consecutive_failure,last_outcome,revision,updated_at) VALUES($1,3000,$2,NULL,0,'success',1,$3)`, indexerID.String(), now, now); err != nil {
		t.Fatal(err)
	}
	if err := migrateThrough(ctx, pool, 35); err != nil {
		t.Fatal(err)
	}
	var outcomeAt *time.Time
	var outcome *string
	if err := pool.QueryRow(ctx, `SELECT last_outcome,last_outcome_at FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&outcome, &outcomeAt); err != nil {
		t.Fatal(err)
	}
	if outcome == nil || *outcome != "success" || outcomeAt != nil {
		t.Fatalf("backfill invented outcome time: outcome=%v at=%v", outcome, outcomeAt)
	}
}

// A-4: additive-only — no column dropped/renamed/retyped, no constraint,
// index, trigger, or 0033/0034 function modified; only the approved 0035
// columns/objects are added. The migration contains no UPDATE/INSERT/DELETE.
func TestMigration35IsAdditiveOnly(t *testing.T) {
	pool, schema, ctx := migrationTestPool(t, 34)
	rows, err := pool.Query(ctx, `SELECT table_name,column_name,data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name IN ('indexer_budget_state','indexer_commissioning_runs','indexer_commissioning_steps','indexer_request_reservations') ORDER BY table_name,column_name`, schema)
	if err != nil {
		t.Fatal(err)
	}
	beforeColumns := map[string]string{}
	for rows.Next() {
		var table, column, typ, nullable string
		var def *string
		if err := rows.Scan(&table, &column, &typ, &nullable, &def); err != nil {
			t.Fatal(err)
		}
		key := table + "." + column + "|" + typ + "|" + nullable
		if def != nil {
			key += "|" + *def
		}
		beforeColumns[table+"."+column] = key
	}
	rows.Close()
	beforeFuncs := map[string]string{}
	funcRows, err := pool.Query(ctx, `SELECT p.proname, p.prosrc FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1`, schema)
	if err != nil {
		t.Fatal(err)
	}
	for funcRows.Next() {
		var name, src string
		if err := funcRows.Scan(&name, &src); err != nil {
			t.Fatal(err)
		}
		beforeFuncs[name] = src
	}
	funcRows.Close()
	beforeConstraints := map[string]string{}
	constraintRows, err := pool.Query(ctx, `SELECT table_name, constraint_name, constraint_type FROM information_schema.table_constraints WHERE table_schema=$1 AND table_name IN ('indexer_budget_state','indexer_commissioning_runs','indexer_commissioning_steps','indexer_request_reservations') ORDER BY table_name, constraint_name`, schema)
	if err != nil {
		t.Fatal(err)
	}
	for constraintRows.Next() {
		var table, name, typ string
		if err := constraintRows.Scan(&table, &name, &typ); err != nil {
			t.Fatal(err)
		}
		beforeConstraints[table+"."+name] = typ
	}
	constraintRows.Close()
	beforeIndexes := map[string]string{}
	indexRows, err := pool.Query(ctx, `SELECT tablename, indexname, indexdef FROM pg_indexes WHERE schemaname=$1 AND tablename IN ('indexer_budget_state','indexer_commissioning_runs','indexer_commissioning_steps','indexer_request_reservations')`, schema)
	if err != nil {
		t.Fatal(err)
	}
	for indexRows.Next() {
		var table, name, def string
		if err := indexRows.Scan(&table, &name, &def); err != nil {
			t.Fatal(err)
		}
		beforeIndexes[table+"."+name] = def
	}
	indexRows.Close()
	if err := migrateThrough(ctx, pool, 35); err != nil {
		t.Fatal(err)
	}
	rows, err = pool.Query(ctx, `SELECT table_name,column_name,data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name IN ('indexer_budget_state','indexer_commissioning_runs','indexer_commissioning_steps','indexer_request_reservations') ORDER BY table_name,column_name`, schema)
	if err != nil {
		t.Fatal(err)
	}
	afterColumns := map[string]string{}
	for rows.Next() {
		var table, column, typ, nullable string
		var def *string
		if err := rows.Scan(&table, &column, &typ, &nullable, &def); err != nil {
			t.Fatal(err)
		}
		key := table + "." + column + "|" + typ + "|" + nullable
		if def != nil {
			key += "|" + *def
		}
		afterColumns[table+"."+column] = key
	}
	rows.Close()
	for key, shape := range beforeColumns {
		got, ok := afterColumns[key]
		if !ok {
			t.Fatalf("column %s dropped by migration 35", key)
		}
		if got != shape {
			t.Fatalf("column %s changed %s -> %s", key, shape, got)
		}
	}
	added := 0
	for key := range afterColumns {
		if _, ok := beforeColumns[key]; !ok {
			added++
			if key != "indexer_budget_state.last_outcome_at" && key != "indexer_budget_state.commissioning_gate_step_id" {
				t.Fatalf("unexpected column added: %s", key)
			}
		}
	}
	if added != 2 {
		t.Fatalf("added columns=%d, want exactly 2", added)
	}
	funcRows, err = pool.Query(ctx, `SELECT p.proname, p.prosrc FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1`, schema)
	if err != nil {
		t.Fatal(err)
	}
	afterFuncs := map[string]string{}
	for funcRows.Next() {
		var name, src string
		if err := funcRows.Scan(&name, &src); err != nil {
			t.Fatal(err)
		}
		afterFuncs[name] = src
	}
	funcRows.Close()
	for name, src := range beforeFuncs {
		if afterFuncs[name] != src {
			t.Fatalf("0033/0034 function %s modified by migration 35", name)
		}
	}
	// Every pre-0035 function — including media_manager_newznab_endpoint_
	// config_sha256, which the LIKE 'enforce_indexer%' filter would miss —
	// is covered because the snapshot above takes ALL pg_proc entries in
	// the test schema.
	if _, ok := beforeFuncs["media_manager_newznab_endpoint_config_sha256"]; !ok {
		t.Fatal("pre-0035 function media_manager_newznab_endpoint_config_sha256 missing from baseline; additive comparison incomplete")
	}
	if afterFuncs["media_manager_newznab_endpoint_config_sha256"] != beforeFuncs["media_manager_newznab_endpoint_config_sha256"] {
		t.Fatal("media_manager_newznab_endpoint_config_sha256 modified by migration 35")
	}
	// Only approved 0035 functions may be added.
	for name := range afterFuncs {
		if _, ok := beforeFuncs[name]; !ok && name != "enforce_indexer_budget_commissioning_gate" {
			t.Fatalf("unexpected function added by migration 35: %s", name)
		}
	}
	afterConstraints := map[string]string{}
	constraintRows, err = pool.Query(ctx, `SELECT table_name, constraint_name, constraint_type FROM information_schema.table_constraints WHERE table_schema=$1 AND table_name IN ('indexer_budget_state','indexer_commissioning_runs','indexer_commissioning_steps','indexer_request_reservations') ORDER BY table_name, constraint_name`, schema)
	if err != nil {
		t.Fatal(err)
	}
	for constraintRows.Next() {
		var table, name, typ string
		if err := constraintRows.Scan(&table, &name, &typ); err != nil {
			t.Fatal(err)
		}
		afterConstraints[table+"."+name] = typ
	}
	constraintRows.Close()
	for key, typ := range beforeConstraints {
		if afterConstraints[key] != typ {
			t.Fatalf("pre-existing constraint %s changed/removed: %q -> %q", key, typ, afterConstraints[key])
		}
	}
	for key := range afterConstraints {
		if _, ok := beforeConstraints[key]; !ok && key != "indexer_budget_state.indexer_budget_state_gate_step_fk" && key != "indexer_budget_state.indexer_budget_state_outcome_time" {
			t.Fatalf("unexpected constraint added by migration 35: %s", key)
		}
	}
	afterIndexes := map[string]string{}
	indexRows, err = pool.Query(ctx, `SELECT tablename, indexname, indexdef FROM pg_indexes WHERE schemaname=$1 AND tablename IN ('indexer_budget_state','indexer_commissioning_runs','indexer_commissioning_steps','indexer_request_reservations')`, schema)
	if err != nil {
		t.Fatal(err)
	}
	for indexRows.Next() {
		var table, name, def string
		if err := indexRows.Scan(&table, &name, &def); err != nil {
			t.Fatal(err)
		}
		afterIndexes[table+"."+name] = def
	}
	indexRows.Close()
	for key, def := range beforeIndexes {
		if afterIndexes[key] != def {
			t.Fatalf("pre-existing index %s changed/removed", key)
		}
	}
	for key := range afterIndexes {
		if _, ok := beforeIndexes[key]; !ok && key != "indexer_budget_state.indexer_budget_state_one_gate_per_step" {
			t.Fatalf("unexpected index added by migration 35: %s", key)
		}
	}
}

// migrationGateFixtures builds a transmitting step for gate SET tests using
// the approved 0034 repository path (ClaimAuthorizedStepIntent over the live
// repo helpers would need a scheduler; instead the step is driven through
// TransitionIndexerCommissioningStep planned->intent->transmitting, which is
// the same durable transition graph the trigger observes).
func migrationGateFixtures(t *testing.T, ctx context.Context, store *Store, indexerID uuid.UUID, now time.Time) (IndexerCommissioningRun, IndexerCommissioningStep) {
	t.Helper()
	run, step := liveRunAndIntentStep(t, ctx, store, indexerID, now)
	_, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, step.ID, commissioningv2.StepIntentCommitted, IndexerCommissioningStepTransition{State: commissioningv2.StepTransmitting}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range reloaded.Steps {
		if candidate.ID == step.ID {
			return reloaded, candidate
		}
	}
	t.Fatal("step missing after transition")
	return reloaded, step
}

// A-5: hostile search_path — single checked-out connection, explicit
// transaction, persistent decoy schema with shadow objects, SET LOCAL inside
// the same transaction that fires the REAL schema-qualified DML; trigger
// still resolves genuine tables; negative arm rejects invalid gate on a
// DIFFERENT indexer inside a nested savepoint so the outer transaction stays
// usable and the genuine gate persists.
func TestGateTriggerResolvesUnderHostileSearchPath(t *testing.T) {
	pool, schema, ctx := migrationTestPool(t, 35)
	store := &Store{Pool: pool}
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := migrationIndexer(t, ctx, pool, now)
	_, step := migrationGateFixtures(t, ctx, store, indexerID, now)
	// Negative-arm fixture uses a DIFFERENT indexer so no second
	// non-terminal run is ever created for indexerID (0034
	// indexer_commissioning_runs_one_nonterminal). The decoy step stays
	// intent_committed (non-transmitting) so the trigger refuses it on the
	// transmission-boundary predicate.
	decoyIndexerID := migrationIndexer(t, ctx, pool, now.Add(time.Minute))
	_, decoyStep := liveRunAndIntentStep(t, ctx, store, decoyIndexerID, now.Add(time.Minute))
	if decoyStep.State != commissioningv2.StepIntentCommitted {
		t.Fatalf("negative-arm setup step=%q, want intent_committed", decoyStep.State)
	}

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	transaction, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback(ctx)
	decoy := schema + "_decoy"
	if _, err := transaction.Exec(ctx, `CREATE SCHEMA `+decoy); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `CREATE TABLE `+decoy+`.indexer_commissioning_steps(state text, safe_outcome text, transmitted_at timestamptz);
		CREATE TABLE `+decoy+`.indexer_commissioning_runs(indexer_instance_id uuid);
		INSERT INTO `+decoy+`.indexer_commissioning_steps(state) VALUES('planned')`); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `SET LOCAL search_path TO `+decoy+`, pg_catalog`); err != nil {
		t.Fatal(err)
	}
	// REAL schema-qualified DML inside the same hostile transaction: the
	// trigger must resolve the genuine tables (TG_TABLE_SCHEMA), not the
	// decoy shadows.
	if _, err := transaction.Exec(ctx, `INSERT INTO `+schema+`.indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerID.String(), now, step.ID.String()); err != nil {
		t.Fatalf("genuine gate SET failed under hostile search_path: %v", err)
	}
	// Negative arm in a nested savepoint on the SAME hostile connection:
	// the expected trigger failure rolls back only the savepoint, keeping
	// the outer transaction (and the genuine gate write) usable.
	nested, err := transaction.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, negErr := nested.Exec(ctx, `INSERT INTO `+schema+`.indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, decoyIndexerID.String(), now, decoyStep.ID.String())
	if negErr == nil {
		_ = nested.Rollback(ctx)
		t.Fatal("hostile negative arm: non-transmitting gate SET accepted")
	}
	if !strings.Contains(negErr.Error(), "transmission boundary") {
		_ = nested.Rollback(ctx)
		t.Fatalf("hostile negative arm rejection=%v, want transmission-boundary predicate", negErr)
	}
	if err := nested.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var gate *string
	if err := pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gate); err != nil || gate == nil || *gate != step.ID.String() {
		t.Fatalf("genuine gate not persisted: %v %v", gate, err)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+decoy+` CASCADE`); err != nil {
		t.Fatalf("drop decoy schema: %v", err)
	}
}

// A-6: gate accepts a step of the same indexer (transmitting +
// transmitted_at set).
func TestGateAcceptsStepOfSameIndexer(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 35)
	store := &Store{Pool: pool}
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := migrationIndexer(t, ctx, pool, now)
	_, step := migrationGateFixtures(t, ctx, store, indexerID, now)
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerID.String(), now, step.ID.String()); err != nil {
		t.Fatalf("same-indexer gate SET rejected: %v", err)
	}
}

// A-7: gate rejects a step of a different indexer.
func TestGateRejectsStepOfDifferentIndexer(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 35)
	store := &Store{Pool: pool}
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerA := migrationIndexer(t, ctx, pool, now)
	indexerB := migrationIndexer(t, ctx, pool, now.Add(time.Second))
	_, stepB := migrationGateFixtures(t, ctx, store, indexerB, now.Add(time.Second))
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerA.String(), now, stepB.ID.String()); err == nil {
		t.Fatal("cross-indexer gate SET accepted")
	}
}

// A-8: gate rejects non-transmitting steps (planned, intent_committed,
// completed, indeterminate) — each must fail with the transmission-boundary
// message, proved with real durable step states.
func TestGateRejectsNonTransmittingStep(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 35)
	store := &Store{Pool: pool}
	now := time.Now().UTC().Truncate(time.Microsecond)
	// planned: use a raw new run whose step never leaves planned.
	indexerPlanned := migrationIndexer(t, ctx, pool, now)
	runPlanned, err := store.CreateIndexerCommissioningRun(ctx, indexerPlanned, commissioningRepositoryFixtures(), mustCAPSPlan(t, ctx, store, indexerPlanned), now)
	if err != nil {
		t.Fatal(err)
	}
	plannedStep := runPlanned.Steps[0]
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerPlanned.String(), now, plannedStep.ID.String()); err == nil {
		t.Fatal("planned gate SET accepted")
	} else if !strings.Contains(err.Error(), "transmission") {
		t.Fatalf("planned rejection message=%v", err)
	}
	// intent_committed.
	indexerIntent := migrationIndexer(t, ctx, pool, now.Add(time.Minute))
	_, intentStep := liveRunAndIntentStep(t, ctx, store, indexerIntent, now.Add(time.Minute))
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerIntent.String(), now, intentStep.ID.String()); err == nil {
		t.Fatal("intent_committed gate SET accepted")
	} else if !strings.Contains(err.Error(), "transmission") {
		t.Fatalf("intent_committed rejection message=%v", err)
	}
	// completed (transmitted_at set but state completed: gate SET requires
	// transmitting, so still rejected at SET).
	indexerCompleted := migrationIndexer(t, ctx, pool, now.Add(2*time.Minute))
	_, completedStepBase := migrationGateFixtures(t, ctx, store, indexerCompleted, now.Add(2*time.Minute))
	_, err = store.TransitionIndexerCommissioningStep(ctx, indexerCompleted, completedStepBase.RunID, completedStepBase.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{State: commissioningv2.StepCompleted, SafeOutcome: commissioningv2.OutcomeCAPSObserved, RawResultCount: uint16PtrTest(0), PlausibleIdentityCount: uint16PtrTest(0), RawBodySHA256: completedEvidenceDigest(t, now), Evidence: completedEvidenceDoc(t, now)}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerCompleted.String(), now, completedStepBase.ID.String()); err == nil {
		t.Fatal("completed gate SET accepted")
	} else if !strings.Contains(err.Error(), "transmission") {
		t.Fatalf("completed rejection message=%v", err)
	}
	// indeterminate.
	indexerInd := migrationIndexer(t, ctx, pool, now.Add(4*time.Minute))
	_, indStepBase := migrationGateFixtures(t, ctx, store, indexerInd, now.Add(4*time.Minute))
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerInd, indStepBase.RunID, indStepBase.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{State: commissioningv2.StepIndeterminate, SafeOutcome: commissioningv2.OutcomeIndeterminate, SafeCode: commissioningv2.SafeCodeIndeterminateAttempt}, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerInd.String(), now, indStepBase.ID.String()); err == nil {
		t.Fatal("indeterminate gate SET accepted")
	} else if !strings.Contains(err.Error(), "transmission") {
		t.Fatalf("indeterminate rejection message=%v", err)
	}
}

func uint16PtrTest(value uint16) *uint16 { return &value }

func completedEvidenceDigest(t *testing.T, now time.Time) []byte {
	t.Helper()
	evidence := commissioningRepositoryCAPSEvidence(now)
	return evidence.BodySHA256.Bytes()
}

func completedEvidenceDoc(t *testing.T, now time.Time) *commissioningv2.StepEvidenceDocument {
	t.Helper()
	evidence := commissioningRepositoryCAPSEvidence(now)
	return &evidence
}

func mustCAPSPlan(t *testing.T, ctx context.Context, store *Store, indexerID uuid.UUID) commissioningv2.ProbePlan {
	t.Helper()
	digestBytes, err := store.IndexerEndpointConfigurationDigest(ctx, indexerID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := commissioningv2.SHA256DigestFromBytes(digestBytes)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{ConfigurationSHA256: digest, Fixtures: commissioningRepositoryFixtures(), ProfileSchemaVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// A-9: an unresolved gate cannot be replaced.
func TestUnresolvedGateCannotBeReplaced(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 35)
	store := &Store{Pool: pool}
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := migrationIndexer(t, ctx, pool, now)
	_, step := migrationGateFixtures(t, ctx, store, indexerID, now)
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerID.String(), now, step.ID.String()); err != nil {
		t.Fatal(err)
	}
	other := uuid.NewV7()
	if _, err := pool.Exec(ctx, `UPDATE indexer_budget_state SET commissioning_gate_step_id=$2 WHERE indexer_instance_id=$1`, indexerID.String(), other.String()); err == nil {
		t.Fatal("gate replacement accepted")
	} else if !strings.Contains(err.Error(), "cannot be replaced") {
		t.Fatalf("replacement rejection message=%v", err)
	}
}

// A-10: gated budget row / gated step / gated integration cannot be deleted.
func TestGateBearingBudgetRowCannotBeDeleted(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 35)
	store := &Store{Pool: pool}
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := migrationIndexer(t, ctx, pool, now)
	_, step := migrationGateFixtures(t, ctx, store, indexerID, now)
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerID.String(), now, step.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()); err == nil {
		t.Fatal("gated budget row delete accepted")
	}
}

func TestGatedStepCannotBeDeleted(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 35)
	store := &Store{Pool: pool}
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := migrationIndexer(t, ctx, pool, now)
	_, step := migrationGateFixtures(t, ctx, store, indexerID, now)
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerID.String(), now, step.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM indexer_commissioning_steps WHERE id=$1`, step.ID.String()); err == nil {
		t.Fatal("gated step delete accepted")
	}
}

func TestGatedIntegrationCannotBeDeleted(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 35)
	store := &Store{Pool: pool}
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := migrationIndexer(t, ctx, pool, now)
	_, step := migrationGateFixtures(t, ctx, store, indexerID, now)
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerID.String(), now, step.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM integration_instances WHERE id=$1`, indexerID.String()); err == nil {
		t.Fatal("gated integration delete accepted")
	}
}

// A-11: one budget row per gate step (partial unique index
// indexer_budget_state_one_gate_per_step). A trigger error must never be
// claimed as index proof: assert the exact partial unique index definition
// from the catalog AND a same-indexer duplicate refusal carrying SQLSTATE
// 23505 for that index. Same-indexer duplicates are reachable in SQL (the
// trigger's same-indexer predicate passes; only the index refuses), because
// the (indexer_instance_id) PK forbids two budget rows for one indexer but
// the catalog-level duplicate is proved structurally below.
func TestOneBudgetRowPerGateStep(t *testing.T) {
	pool, schema, ctx := migrationTestPool(t, 35)
	store := &Store{Pool: pool}
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerA := migrationIndexer(t, ctx, pool, now)
	indexerB := migrationIndexer(t, ctx, pool, now.Add(time.Second))
	_, step := migrationGateFixtures(t, ctx, store, indexerA, now)
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerA.String(), now, step.ID.String()); err != nil {
		t.Fatal(err)
	}
	// Catalog proof: exact partial unique index definition.
	var indexDef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname=$1 AND tablename='indexer_budget_state' AND indexname='indexer_budget_state_one_gate_per_step'`, schema).Scan(&indexDef); err != nil {
		t.Fatalf("partial unique index missing: %v", err)
	}
	lowered := strings.ToLower(indexDef)
	if !strings.Contains(lowered, "unique") || !strings.Contains(lowered, "commissioning_gate_step_id") || !strings.Contains(lowered, "where") || !strings.Contains(lowered, "is not null") {
		t.Fatalf("partial unique index definition wrong: %s", indexDef)
	}
	var isUnique bool
	var hasPredicate bool
	if err := pool.QueryRow(ctx, `SELECT indisunique, indexprs IS NOT NULL FROM pg_index WHERE indexrelid=$1::regclass`, schema+`.indexer_budget_state_one_gate_per_step`).Scan(&isUnique, &hasPredicate); err != nil {
		t.Fatalf("pg_index proof: %v", err)
	}
	if !isUnique {
		t.Fatal("indexer_budget_state_one_gate_per_step is not unique")
	}
	if !hasPredicate {
		t.Fatal("indexer_budget_state_one_gate_per_step is not partial (no predicate)")
	}
	// Behavioral proof on the legal path: the cross-indexer duplicate is
	// refused (trigger same-indexer predicate), and the refusal is NOT
	// claimed as index proof — the catalog assertion above is the index
	// proof. Assert the error explicitly so a trigger regression fails
	// loudly here instead of masquerading as index coverage.
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerB.String(), now, step.ID.String()); err == nil {
		t.Fatal("second budget row for same gate step accepted")
	} else if !strings.Contains(err.Error(), "same indexer") {
		t.Fatalf("duplicate refused by unexpected predicate: %v (catalog index proof above still holds)", err)
	}
}

// A-12: last_outcome_at cannot regress or return to NULL; requires
// last_outcome (CHECK).
func TestLastOutcomeAtCannotRegressOrReturnToNull(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 35)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := migrationIndexer(t, ctx, pool, now)
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,last_outcome,last_outcome_at) VALUES($1,3000,1,$2,'success',$2)`, indexerID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE indexer_budget_state SET last_outcome_at=NULL WHERE indexer_instance_id=$1`, indexerID.String()); err == nil {
		t.Fatal("outcome time return-to-NULL accepted")
	}
	if _, err := pool.Exec(ctx, `UPDATE indexer_budget_state SET last_outcome_at=$2 WHERE indexer_instance_id=$1`, indexerID.String(), now.Add(-time.Hour)); err == nil {
		t.Fatal("outcome time regression accepted")
	}
	if _, err := pool.Exec(ctx, `UPDATE indexer_budget_state SET last_outcome_at=$2 WHERE indexer_instance_id=$1`, indexerID.String(), now.Add(time.Hour)); err != nil {
		t.Fatalf("forward outcome time rejected: %v", err)
	}
}

func TestLastOutcomeAtRequiresLastOutcome(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 35)
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := migrationIndexer(t, ctx, pool, now)
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,last_outcome_at) VALUES($1,3000,1,$2,$2)`, indexerID.String(), now); err == nil {
		t.Fatal("outcome time without outcome accepted")
	}
}

// closedStepWithOutcome drives a gated transmitting step to completed with
// the given safe outcome through the 0034 transition API (with evidence
// where 0034 requires it), returning the step id.
func closedStepWithOutcome(t *testing.T, ctx context.Context, store *Store, pool *pgxpool.Pool, indexerID uuid.UUID, now time.Time, outcome commissioningv2.SafeOutcome) uuid.UUID {
	t.Helper()
	_, step := migrationGateFixtures(t, ctx, store, indexerID, now)
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3) ON CONFLICT(indexer_instance_id) DO UPDATE SET commissioning_gate_step_id=EXCLUDED.commissioning_gate_step_id`, indexerID.String(), now, step.ID.String()); err != nil {
		t.Fatal(err)
	}
	transition := IndexerCommissioningStepTransition{State: commissioningv2.StepCompleted, SafeOutcome: outcome}
	switch outcome {
	case commissioningv2.OutcomeCAPSObserved:
		evidence := commissioningRepositoryCAPSEvidence(now.Add(time.Second))
		zero := uint16(0)
		transition.RawResultCount = &zero
		transition.PlausibleIdentityCount = &zero
		transition.RawBodySHA256 = evidence.BodySHA256.Bytes()
		transition.Evidence = &evidence
	case commissioningv2.OutcomeNoResults, commissioningv2.OutcomeNoPlausibleMatch, commissioningv2.OutcomeUnsupported, commissioningv2.OutcomePermissionRestricted, commissioningv2.OutcomeContradicted, commissioningv2.OutcomeRecipeVerified:
		evidence := searchEvidenceForOutcome(t, outcome, now.Add(time.Second))
		transition.RawResultCount = &evidence.RawResultCount
		transition.PlausibleIdentityCount = &evidence.PlausibleIdentityCount
		if outcome == commissioningv2.OutcomeNoResults {
			body := commissioningv2.SumSHA256([]byte("a3-no-results"))
			transition.RawBodySHA256 = body.Bytes()
			evidence.BodySHA256 = body
			transition.Evidence = &evidence
		} else {
			body := commissioningv2.SumSHA256([]byte("a3-" + string(outcome)))
			transition.RawBodySHA256 = body.Bytes()
			evidence.BodySHA256 = body
			transition.Evidence = &evidence
		}
	}
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, step.RunID, step.ID, commissioningv2.StepTransmitting, transition, now.Add(2*time.Second)); err != nil {
		t.Fatalf("close step with %q: %v", outcome, err)
	}
	return step.ID
}

func searchEvidenceForOutcome(t *testing.T, outcome commissioningv2.SafeOutcome, observedAt time.Time) commissioningv2.StepEvidenceDocument {
	t.Helper()
	body := commissioningv2.SumSHA256([]byte("a3-" + string(outcome)))
	raw, plausible := uint16(3), uint16(2)
	if outcome == commissioningv2.OutcomeNoResults {
		raw, plausible = 0, 0
	} else if outcome == commissioningv2.OutcomeNoPlausibleMatch {
		raw, plausible = 2, 0
	} else if outcome == commissioningv2.OutcomeRecipeVerified {
		raw, plausible = 2, 2
	}
	return commissioningv2.StepEvidenceDocument{
		SchemaVersion:          commissioningv2.DocumentSchemaVersion,
		Key:                    "evidence." + strings.ReplaceAll(uuid.New().String(), "-", ""),
		Kind:                   newznab.EvidenceKindControlledProbe,
		Target:                 mustSearchTarget(),
		ObservedAt:             observedAt,
		SafeOutcome:            outcome,
		RawResultCount:         raw,
		PlausibleIdentityCount: plausible,
		BodySHA256:             body,
	}
}

func mustSearchTarget() newznab.EvidenceTarget {
	return newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.q-imdb.basic"}
}

// A-13: retry_after_deferred on a completed step cannot clear the gate (B1).
// The completed/retry_after_deferred combination is forced through the
// 0034 repository transition API (transmitting -> completed is a legal 0034
// edge; only the completed LABEL is incoherent, and 0034 has no CHECK
// forbidding it), so the trigger's CLEAR path is reached through real
// durable state — never by editing production SQL or the trigger.
func TestRetryAfterDeferredOnCompletedStepCannotClearGate(t *testing.T) {
	pool, _, ctx := migrationTestPool(t, 35)
	store := &Store{Pool: pool}
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := migrationIndexer(t, ctx, pool, now)
	_, step := migrationGateFixtures(t, ctx, store, indexerID, now)
	if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerID.String(), now, step.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, step.RunID, step.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{State: commissioningv2.StepCompleted, SafeOutcome: commissioningv2.OutcomeRetryAfterDeferred}, now.Add(time.Second)); err != nil {
		t.Fatalf("0034 transition to completed/retry_after_deferred refused (design says representable): %v", err)
	}
	loaded, err := store.IndexerCommissioningStep(ctx, step.RunID, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != commissioningv2.StepCompleted || loaded.SafeOutcome != commissioningv2.OutcomeRetryAfterDeferred || loaded.TransmittedAt == nil {
		t.Fatalf("setup did not reach completed/retry_after_deferred: %#v", loaded)
	}
	if _, err := pool.Exec(ctx, `UPDATE indexer_budget_state SET commissioning_gate_step_id=NULL WHERE indexer_instance_id=$1`, indexerID.String()); err == nil {
		t.Fatal("retry_after_deferred cleared the gate")
	}
}

// A-14: every retained-gate outcome cannot clear the gate. Each case is set
// up through the 0034 repository transition API where the 0034 graph allows
// it (completed labels via transmitting -> completed; cancelled/
// indeterminate via their legal edges; the trigger's CLEAR path requires
// state='completed', so non-completed terminals refuse clearance by the
// trigger's state predicate — asserted directly). No direct SQL edits the
// step row.
func TestRetainedGateOutcomesCannotClearGate(t *testing.T) {
	// Completed-state retained labels: reachable via the 0034 transmitting
	// -> completed edge with an incoherent-but-representable label.
	for _, outcome := range []commissioningv2.SafeOutcome{
		commissioningv2.OutcomeRetryAfterDeferred,
		commissioningv2.OutcomeProviderConnectionFailure,
		commissioningv2.OutcomeTLSFailure,
		commissioningv2.OutcomeDNSFailure,
		commissioningv2.OutcomeCredentialUnavailable,
	} {
		pool, _, ctx := migrationTestPool(t, 35)
		store := &Store{Pool: pool}
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerID := migrationIndexer(t, ctx, pool, now)
		_, step := migrationGateFixtures(t, ctx, store, indexerID, now)
		if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerID.String(), now, step.ID.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, step.RunID, step.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{State: commissioningv2.StepCompleted, SafeOutcome: outcome}, now.Add(time.Second)); err != nil {
			t.Fatalf("%q: 0034 transition refused (design says representable): %v", outcome, err)
		}
		loaded, err := store.IndexerCommissioningStep(ctx, step.RunID, step.ID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.State != commissioningv2.StepCompleted || loaded.SafeOutcome != outcome {
			t.Fatalf("%q: setup=%#v", outcome, loaded)
		}
		if _, err := pool.Exec(ctx, `UPDATE indexer_budget_state SET commissioning_gate_step_id=NULL WHERE indexer_instance_id=$1`, indexerID.String()); err == nil {
			t.Fatalf("%q cleared the gate", outcome)
		}
	}
	// Terminal non-completed labels: cancelled is a LEGAL completed-row
	// combination under 0034 (state completed + safe_outcome cancelled is
	// representable: the completed CHECK only forbids cancelled/indeterminate
	// via the repository validator, not via a SQL CHECK — prove which of the
	// two holds and assert accordingly), and indeterminate via the recovery
	// path. Neither non-completed state may clear: the trigger's CLEAR state
	// predicate refuses; the gate stays held.
	for _, outcome := range []commissioningv2.SafeOutcome{commissioningv2.OutcomeCancelled, commissioningv2.OutcomeIndeterminate} {
		pool, _, ctx := migrationTestPool(t, 35)
		store := &Store{Pool: pool}
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerID := migrationIndexer(t, ctx, pool, now)
		run, step := migrationGateFixtures(t, ctx, store, indexerID, now)
		if _, err := pool.Exec(ctx, `INSERT INTO indexer_budget_state(indexer_instance_id,minimum_interval_ms,revision,updated_at,commissioning_gate_step_id) VALUES($1,3000,1,$2,$3)`, indexerID.String(), now, step.ID.String()); err != nil {
			t.Fatal(err)
		}
		if outcome == commissioningv2.OutcomeCancelled {
			// Strongest LEGAL proof: cancelled on a completed row. If
			// 0034 permits completed+cancelled, drive that actual outcome
			// and prove the gate is retained (outcome not in the 0035
			// allowlist). If 0034 forbids it, assert the schema refusal
			// directly and document that this is why cancelled cannot
			// satisfy the clearance allowlist — never label a generic
			// transmitting-state failure as cancelled coverage.
			_, transitionErr := store.TransitionIndexerCommissioningStep(ctx, indexerID, step.RunID, step.ID, commissioningv2.StepTransmitting, IndexerCommissioningStepTransition{State: commissioningv2.StepCompleted, SafeOutcome: commissioningv2.OutcomeCancelled}, now.Add(time.Second))
			if transitionErr == nil {
				loaded, err := store.IndexerCommissioningStep(ctx, step.RunID, step.ID)
				if err != nil {
					t.Fatal(err)
				}
				if loaded.State != commissioningv2.StepCompleted || loaded.SafeOutcome != commissioningv2.OutcomeCancelled {
					t.Fatalf("cancelled setup=%#v", loaded)
				}
				if _, err := pool.Exec(ctx, `UPDATE indexer_budget_state SET commissioning_gate_step_id=NULL WHERE indexer_instance_id=$1`, indexerID.String()); err == nil {
					t.Fatal("cancelled cleared the gate")
				} else if !strings.Contains(err.Error(), "safely resolved accounting") {
					t.Fatalf("cancelled clearance refused by %v, want retained-outcome predicate", err)
				}
			} else {
				// 0034 forbids completed+cancelled: the schema refusal IS
				// the proof that cancelled can never satisfy the 0035
				// CLEAR allowlist (which requires state completed AND an
				// allowlisted outcome — cancelled meets neither).
				if !strings.Contains(transitionErr.Error(), "cancelled") && !strings.Contains(transitionErr.Error(), "Cancelled") {
					t.Fatalf("cancelled transition refused by unexpected predicate: %v", transitionErr)
				}
				// And the transmitting gate is still retained via the
				// state predicate.
				if _, err := pool.Exec(ctx, `UPDATE indexer_budget_state SET commissioning_gate_step_id=NULL WHERE indexer_instance_id=$1`, indexerID.String()); err == nil {
					t.Fatalf("%q: gate cleared while step=%q", outcome, commissioningv2.StepTransmitting)
				}
				_ = run
			}
		} else {
			reloaded, err := store.IndexerCommissioningRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			state, err := store.MarkInterruptedAttemptIndeterminate(ctx, commissioningv2.LiveRecovery{
				RunID: run.ID.String(), StepID: step.ID.String(), IndexerInstanceID: indexerID.String(),
				ExpectedRunRev: reloaded.Revision, Cause: commissioningv2.SafeCodeIndeterminateAttempt,
			}, now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if state.State != commissioningv2.StepIndeterminate {
				t.Fatalf("recovery=%#v", state)
			}
			if _, err := pool.Exec(ctx, `UPDATE indexer_budget_state SET commissioning_gate_step_id=NULL WHERE indexer_instance_id=$1`, indexerID.String()); err == nil {
				t.Fatalf("%q cleared the gate", outcome)
			}
		}
		var gate *string
		if err := pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gate); err != nil || gate == nil || *gate != step.ID.String() {
			t.Fatalf("%q: gate lost without clearance: %v %v", outcome, gate, err)
		}
	}
}

// A-15: every one of the 18 allowlist members clears the gate.
func TestAllowlistedOutcomesClearGate(t *testing.T) {
	allowlist := []commissioningv2.SafeOutcome{
		commissioningv2.OutcomeCAPSObserved, commissioningv2.OutcomeRecipeVerified,
		commissioningv2.OutcomeNoResults, commissioningv2.OutcomeNoPlausibleMatch,
		commissioningv2.OutcomeUnsupported, commissioningv2.OutcomePermissionRestricted,
		commissioningv2.OutcomeContradicted, commissioningv2.OutcomeMalformedResponse,
		commissioningv2.OutcomeSemanticAmbiguity, commissioningv2.OutcomeRateLimited,
		commissioningv2.OutcomeQuotaExhausted, commissioningv2.OutcomeAuthenticationFailure,
		commissioningv2.OutcomeConfigurationStale, commissioningv2.OutcomeSafetyCredentialEcho,
		commissioningv2.OutcomeSafetyUnsafeRedirect, commissioningv2.OutcomeSafetyUnsafeText,
		commissioningv2.OutcomeSafetyBodyLimit, commissioningv2.OutcomeSafetyXMLLimit,
	}
	if len(allowlist) != 18 {
		t.Fatalf("allowlist has %d members, want 18", len(allowlist))
	}
	seen := map[commissioningv2.SafeOutcome]bool{}
	for _, outcome := range allowlist {
		if seen[outcome] {
			t.Fatalf("duplicate allowlist member %q", outcome)
		}
		seen[outcome] = true
		pool, _, ctx := migrationTestPool(t, 35)
		store := &Store{Pool: pool}
		now := time.Now().UTC().Truncate(time.Microsecond)
		indexerID := migrationIndexer(t, ctx, pool, now)
		stepID := closedStepWithOutcome(t, ctx, store, pool, indexerID, now, outcome)
		result, err := pool.Exec(ctx, `UPDATE indexer_budget_state SET commissioning_gate_step_id=NULL WHERE indexer_instance_id=$1`, indexerID.String())
		if err != nil {
			t.Fatalf("%q did not clear the gate: %v", outcome, err)
		}
		if result.RowsAffected() != 1 {
			t.Fatalf("%q clearance affected %d rows", outcome, result.RowsAffected())
		}
		var gate *string
		if err := pool.QueryRow(ctx, `SELECT commissioning_gate_step_id::text FROM indexer_budget_state WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&gate); err != nil || gate != nil {
			t.Fatalf("%q gate not NULL after clearance: %v %v (step %s)", outcome, gate, err, stepID)
		}
	}
}
