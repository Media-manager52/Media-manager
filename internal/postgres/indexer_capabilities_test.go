package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"uuid"
)

func TestCanonicalNewznabConfigCategoryReviewAndProviderContract(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantMovie    []int
		wantSeries   []int
		wantReview   bool
		wantContract string
		wantError    bool
	}{
		{
			name:       "legacy categories require review",
			input:      `{"base_url":"https://indexer.invalid/api","categories":[2000,2040]}`,
			wantMovie:  []int{2000, 2040},
			wantSeries: []int{2000, 2040},
			wantReview: true,
		},
		{
			name:       "explicit split categories preserve owner review",
			input:      `{"base_url":"https://indexer.invalid/api","movie_category_ids":[2040,2000,2040],"series_category_ids":[5040,5000,5040],"category_selection_review_required":false}`,
			wantMovie:  []int{2000, 2040},
			wantSeries: []int{5000, 5040},
			wantReview: false,
		},
		{
			name:       "missing review flag remains gated",
			input:      `{"base_url":"https://indexer.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000]}`,
			wantMovie:  []int{2000},
			wantSeries: []int{5000},
			wantReview: true,
		},
		{
			name:       "incomplete split selection remains gated",
			input:      `{"base_url":"https://indexer.invalid/api","categories":[5000],"movie_category_ids":[2000],"category_selection_review_required":false}`,
			wantMovie:  []int{2000},
			wantSeries: []int{5000},
			wantReview: true,
		},
		{
			name:      "malformed category set",
			input:     `{"base_url":"https://indexer.invalid/api","movie_category_ids":{"bad":true},"series_category_ids":[]}`,
			wantError: true,
		},
		{
			name:      "null category set",
			input:     `{"base_url":"https://indexer.invalid/api","movie_category_ids":null,"series_category_ids":[]}`,
			wantError: true,
		},
		{
			name:         "registered contract and accepted origin",
			input:        `{"base_url":"https://nzbfinder.ws/api","movie_category_ids":[],"series_category_ids":[],"category_selection_review_required":false,"provider_contract_key":"nzbfinder"}`,
			wantMovie:    []int{},
			wantSeries:   []int{},
			wantReview:   false,
			wantContract: "nzbfinder",
		},
		{
			name:      "registered contract on unrelated origin",
			input:     `{"base_url":"https://indexer.invalid/api","movie_category_ids":[],"series_category_ids":[],"category_selection_review_required":false,"provider_contract_key":"nzbfinder"}`,
			wantError: true,
		},
		{
			name:      "unknown contract",
			input:     `{"base_url":"https://indexer.invalid/api","provider_contract_key":"unknown"}`,
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, _, err := canonicalNewznabConfig(json.RawMessage(test.input), false)
			if test.wantError {
				if err == nil {
					t.Fatalf("canonicalNewznabConfig() unexpectedly succeeded: %s", body)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Movie          []int  `json:"movie_category_ids"`
				Series         []int  `json:"series_category_ids"`
				ReviewRequired bool   `json:"category_selection_review_required"`
				ModeVerified   bool   `json:"mode_verified"`
				Contract       string `json:"provider_contract_key"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if !equalIntSet(got.Movie, test.wantMovie) || !equalIntSet(got.Series, test.wantSeries) || got.ReviewRequired != test.wantReview || got.ModeVerified || got.Contract != test.wantContract {
				t.Fatalf("canonical configuration = %s", body)
			}
		})
	}
	tooManyCategories := make([]any, 129)
	if _, err := newznabCategorySet(tooManyCategories); err == nil {
		t.Fatal("category set above the fixed bound was accepted")
	}
	exactCategory := int64(9_007_199_254_740_993)
	body, _, err := canonicalNewznabConfig(json.RawMessage(`{"base_url":"https://indexer.invalid/api","movie_category_ids":[9007199254740993],"series_category_ids":[],"category_selection_review_required":false}`), false)
	if err != nil {
		t.Fatal(err)
	}
	var exact struct {
		Movie []int64 `json:"movie_category_ids"`
	}
	if err := json.Unmarshal(body, &exact); err != nil || len(exact.Movie) != 1 || exact.Movie[0] != exactCategory {
		t.Fatalf("large integer category was not preserved exactly: %s, %v", body, err)
	}
	body, _, err = canonicalNewznabConfig(json.RawMessage(`{"base_url":"https://indexer.invalid/api","categories":[2000],"minimum_interval_ms":4000,"query_limit":25,"grab_limit":5,"budget_window_seconds":3600,"benign_note":"preserve-me"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	var preserved map[string]any
	if err := json.Unmarshal(body, &preserved); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{"minimum_interval_ms": float64(4000), "query_limit": float64(25), "grab_limit": float64(5), "budget_window_seconds": float64(3600), "benign_note": "preserve-me"} {
		if got := preserved[key]; got != want {
			t.Fatalf("preserved %s=%v want %v", key, got, want)
		}
	}
	for _, test := range []struct {
		name      string
		endpoint  string
		allowHTTP bool
		want      string
	}{
		{name: "HTTPS host and default port", endpoint: "HTTPS://INDEXER.INVALID:443/api/", want: "https://indexer.invalid/api"},
		{name: "HTTP host and default port", endpoint: "http://INDEXER.INVALID:80/api/", allowHTTP: true, want: "http://indexer.invalid/api"},
		{name: "zero-padded default port", endpoint: "https://INDEXER.INVALID:0443/api", want: "https://indexer.invalid/api"},
		{name: "IPv6 literal", endpoint: "https://[2001:DB8::1]:443/api/", want: "https://[2001:db8::1]/api"},
	} {
		t.Run("canonical endpoint "+test.name, func(t *testing.T) {
			got, err := canonicalNewznabEndpoint(test.endpoint, test.allowHTTP)
			if err != nil || got != test.want {
				t.Fatalf("canonical endpoint=%q want=%q err=%v", got, test.want, err)
			}
		})
	}
}

func TestIndexerOperationStatusValidationRejectsItemScopeAndUnsafeFields(t *testing.T) {
	base := IndexerOperationStatus{
		IndexerInstanceID:   uuid.NewV7(),
		CapabilityProfileID: uuid.NewV7(),
		OperationKey:        "search",
		RecipeKey:           "movie-imdb",
		Availability:        "degraded",
		LastOutcome:         "failed",
		SafeCode:            "malformed-envelope",
		LastFailureScope:    "operation",
		ConsecutiveFailures: 1,
		UpdatedAt:           time.Now().UTC(),
	}
	if !validIndexerOperationStatus(base) {
		t.Fatal("valid operation-scoped status was rejected")
	}
	itemScoped := base
	itemScoped.LastFailureScope = "item"
	if validIndexerOperationStatus(itemScoped) {
		t.Fatal("item failure was accepted as operation status")
	}
	wrongOperation := base
	wrongOperation.OperationKey = "details"
	if validIndexerOperationStatus(wrongOperation) {
		t.Fatal("non-search operation was accepted with a recipe key")
	}
	wrongOperation.RecipeKey = ""
	wrongOperation.OperationKey = "provider_custom"
	if validIndexerOperationStatus(wrongOperation) {
		t.Fatal("unknown operation key was accepted")
	}
	unsafe := base
	unsafe.SafeCode = "https://provider.invalid/?apikey=secret"
	if validIndexerOperationStatus(unsafe) {
		t.Fatal("unsafe diagnostic text was accepted")
	}
}

func TestIndexerCapabilityPersistenceLifecycle(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	store := openIndexerCapabilityTestStore(t, ctx, databaseURL)
	defer store.Close()

	integration, err := store.CreateIntegration(ctx, Integration{
		Kind:      "newznab",
		Name:      "Capability persistence",
		Config:    json.RawMessage(`{"base_url":"https://indexer.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000],"category_selection_review_required":false}`),
		SecretRef: "NEWZNAB_CAPABILITY_TEST_KEY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIntegration(ctx, Integration{
		Kind:      "newznab",
		Name:      "Canonical endpoint duplicate",
		Config:    json.RawMessage(`{"base_url":"HTTPS://INDEXER.INVALID:443/api/","movie_category_ids":[],"series_category_ids":[],"category_selection_review_required":false}`),
		SecretRef: "NEWZNAB_CAPABILITY_DUPLICATE_KEY",
	}); !errors.Is(err, ErrDuplicateIndexerURL) {
		t.Fatalf("equivalent endpoint duplicate was accepted: %v", err)
	}
	observedEndpointDigest, err := store.IndexerEndpointConfigurationDigest(ctx, integration.ID)
	if err != nil {
		t.Fatal(err)
	}
	document := activatableCapabilityProfileDocument(t)

	const writers = 4
	profiles := make([]IndexerCapabilityProfile, writers)
	errs := make([]error, writers)
	var group sync.WaitGroup
	for index := range profiles {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			profiles[index], errs[index] = store.CreateIndexerCapabilityProfile(ctx, integration.ID, document, observedEndpointDigest, time.Now().UTC())
		}(index)
	}
	group.Wait()
	revisions := make([]int, 0, writers)
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent create %d: %v", index, err)
		}
		revisions = append(revisions, int(profiles[index].Revision))
	}
	sort.Ints(revisions)
	for index, revision := range revisions {
		if revision != index+1 {
			t.Fatalf("concurrent revisions=%v", revisions)
		}
	}

	listed, err := store.ListIndexerCapabilityProfiles(ctx, integration.ID)
	if err != nil || len(listed) != writers {
		t.Fatalf("listed profiles=%d err=%v", len(listed), err)
	}
	if _, err := store.ActiveIndexerCapabilityProfile(ctx, integration.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active profile before activation: %v", err)
	}
	draftStatus := IndexerOperationStatus{
		IndexerInstanceID:   integration.ID,
		CapabilityProfileID: listed[3].ID,
		OperationKey:        "search",
		RecipeKey:           "movie-imdb",
		Availability:        "unknown",
		UpdatedAt:           time.Now().UTC(),
	}
	if err := store.SaveIndexerOperationStatus(ctx, draftStatus); !errors.Is(err, ErrInvalidIndexerOperationStatus) {
		t.Fatalf("status accepted before profile activation: %v", err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_operation_status(indexer_instance_id,capability_profile_id,operation_key,recipe_key,availability,updated_at) VALUES($1,$2,'search','movie-imdb','unknown',now())`, integration.ID.String(), listed[3].ID.String()); err == nil {
		t.Fatal("database accepted status before profile activation")
	}

	first := listed[0]
	activated, err := store.ActivateIndexerCapabilityProfile(ctx, integration.ID, first.ID, "owner", time.Now().UTC())
	if err != nil || activated.LifecycleState != "active" {
		t.Fatalf("activate first=%#v err=%v", activated, err)
	}
	second := listed[1]
	activated, err = store.ActivateIndexerCapabilityProfile(ctx, integration.ID, second.ID, "owner", time.Now().UTC())
	if err != nil || activated.ID != second.ID || activated.LifecycleState != "active" {
		t.Fatalf("activate second=%#v err=%v", activated, err)
	}
	first, err = store.IndexerCapabilityProfile(ctx, first.ID)
	if err != nil || first.LifecycleState != "superseded" || first.SupersededAt == nil {
		t.Fatalf("superseded first=%#v err=%v", first, err)
	}
	active, err := store.ActiveIndexerCapabilityProfile(ctx, integration.ID)
	if err != nil || active.ID != second.ID {
		t.Fatalf("active profile=%#v err=%v", active, err)
	}
	var activeCount int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM indexer_capability_profiles WHERE indexer_instance_id=$1 AND lifecycle_state='active'`, integration.ID.String()).Scan(&activeCount); err != nil || activeCount != 1 {
		t.Fatalf("active profile count=%d err=%v", activeCount, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE indexer_capability_profiles SET lifecycle_state='active',activated_at=now() WHERE id=$1`, listed[2].ID.String()); err == nil {
		t.Fatal("database allowed a second active profile for one indexer")
	}

	now := time.Now().UTC()
	for _, recipeKey := range []string{"", "movie-imdb"} {
		status := IndexerOperationStatus{
			IndexerInstanceID:   integration.ID,
			CapabilityProfileID: second.ID,
			OperationKey:        "search",
			RecipeKey:           recipeKey,
			Availability:        "ready",
			LastOutcome:         "success",
			ConsecutiveFailures: 0,
			LastSuccessAt:       &now,
			UpdatedAt:           now,
		}
		if err := store.SaveIndexerOperationStatus(ctx, status); err != nil {
			t.Fatalf("save recipe %q: %v", recipeKey, err)
		}
		loaded, err := store.LoadIndexerOperationStatus(ctx, integration.ID, second.ID, "search", recipeKey)
		if err != nil || loaded.Availability != "ready" || loaded.RecipeKey != recipeKey {
			t.Fatalf("load recipe %q=%#v err=%v", recipeKey, loaded, err)
		}
	}
	invalidTarget := IndexerOperationStatus{
		IndexerInstanceID:   integration.ID,
		CapabilityProfileID: second.ID,
		OperationKey:        "details",
		RecipeKey:           "movie-imdb",
		Availability:        "unknown",
		UpdatedAt:           time.Now().UTC(),
	}
	if err := store.SaveIndexerOperationStatus(ctx, invalidTarget); !errors.Is(err, ErrInvalidIndexerOperationStatus) {
		t.Fatalf("non-search operation accepted a recipe status: %v", err)
	}
	invalidTarget.OperationKey = "search"
	invalidTarget.RecipeKey = "movie-missing"
	if err := store.SaveIndexerOperationStatus(ctx, invalidTarget); !errors.Is(err, ErrInvalidIndexerOperationStatus) {
		t.Fatalf("status accepted a recipe absent from its profile: %v", err)
	}

	third := listed[2]
	active, err = store.ActivateIndexerCapabilityProfile(ctx, integration.ID, third.ID, "owner", time.Now().UTC())
	if err != nil || active.ID != third.ID {
		t.Fatalf("activate third=%#v err=%v", active, err)
	}
	unknown, err := store.LoadIndexerOperationStatus(ctx, integration.ID, third.ID, "search", "movie-imdb")
	if err != nil || unknown.Availability != "unknown" || !unknown.UpdatedAt.IsZero() {
		t.Fatalf("new-profile status=%#v err=%v", unknown, err)
	}
	statusIdentityUpdates := []struct {
		description string
		statement   string
		arguments   []any
	}{
		{description: "instance", statement: `UPDATE indexer_operation_status SET indexer_instance_id=$2 WHERE capability_profile_id=$1 AND operation_key='search' AND recipe_key=''`, arguments: []any{second.ID.String(), uuid.NewV7().String()}},
		{description: "profile", statement: `UPDATE indexer_operation_status SET capability_profile_id=$2 WHERE capability_profile_id=$1 AND operation_key='search' AND recipe_key=''`, arguments: []any{second.ID.String(), third.ID.String()}},
		{description: "operation", statement: `UPDATE indexer_operation_status SET operation_key='details' WHERE capability_profile_id=$1 AND operation_key='search' AND recipe_key=''`, arguments: []any{second.ID.String()}},
		{description: "recipe", statement: `UPDATE indexer_operation_status SET recipe_key='movie-imdb' WHERE capability_profile_id=$1 AND operation_key='search' AND recipe_key=''`, arguments: []any{second.ID.String()}},
	}
	for _, update := range statusIdentityUpdates {
		if _, err := store.Pool.Exec(ctx, update.statement, update.arguments...); err == nil || !strings.Contains(err.Error(), "indexer operation status identity is immutable") {
			t.Fatalf("database changed status %s identity: %v", update.description, err)
		}
	}

	invalidated, err := store.InvalidateIndexerCapabilityProfile(ctx, integration.ID, listed[3].ID, "configuration_changed", "owner", time.Now().UTC())
	if err != nil || invalidated.LifecycleState != "invalidated" || invalidated.InvalidatedAt == nil {
		t.Fatalf("invalidate=%#v err=%v", invalidated, err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, integration.ID, invalidated.ID, "owner", time.Now().UTC()); !errors.Is(err, ErrInvalidIndexerCapabilityProfile) {
		t.Fatalf("activate invalidated profile: %v", err)
	}

	endpointDraft, err := store.CreateIndexerCapabilityProfile(ctx, integration.ID, document, observedEndpointDigest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{base_url}',to_jsonb('https://changed.invalid/api'::text),true) WHERE id=$1`, integration.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateIndexerCapabilityProfile(ctx, integration.ID, document, observedEndpointDigest, time.Now().UTC()); !errors.Is(err, ErrIndexerEndpointConfigurationChanged) {
		t.Fatalf("create rebound observations after endpoint change: %v", err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, integration.ID, endpointDraft.ID, "owner", time.Now().UTC()); !errors.Is(err, ErrIndexerEndpointConfigurationChanged) {
		t.Fatalf("activate after endpoint change: %v", err)
	}
	if _, err := store.ActiveIndexerCapabilityProfile(ctx, integration.ID); !errors.Is(err, ErrIndexerEndpointConfigurationChanged) {
		t.Fatalf("active load after endpoint change: %v", err)
	}

	states := []struct {
		profile IndexerCapabilityProfile
		state   string
	}{
		{profile: first, state: "superseded"},
		{profile: active, state: "active"},
		{profile: invalidated, state: "invalidated"},
		{profile: endpointDraft, state: "draft"},
	}
	for _, item := range states {
		t.Run("immutable_"+item.state, func(t *testing.T) {
			assertCapabilityProfileImmutable(t, ctx, store, item.profile.ID, item.state)
		})
	}
	if _, err := store.Pool.Exec(ctx, `DELETE FROM integration_instances WHERE id=$1`, integration.ID.String()); err == nil {
		t.Fatal("database deleted an integration with retained capability history")
	}

	var unsafePersistenceCount int
	if err := store.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM indexer_capability_profiles WHERE profile_document::text ILIKE '%indexer.invalid%' OR profile_document::text ILIKE '%NEWZNAB_CAPABILITY_TEST_KEY%')
		+ (SELECT count(*) FROM indexer_operation_status WHERE concat_ws(' ',operation_key,recipe_key,last_outcome,safe_code) ILIKE '%indexer.invalid%' OR concat_ws(' ',operation_key,recipe_key,last_outcome,safe_code) ILIKE '%NEWZNAB_CAPABILITY_TEST_KEY%')
		+ (SELECT count(*) FROM audit_events WHERE action LIKE 'newznab.capability_profile_%' AND (detail::text ILIKE '%indexer.invalid%' OR detail::text ILIKE '%NEWZNAB_CAPABILITY_TEST_KEY%'))`).Scan(&unsafePersistenceCount); err != nil {
		t.Fatal(err)
	}
	if unsafePersistenceCount != 0 {
		t.Fatalf("capability persistence leaked endpoint or secret-reference metadata: rows=%d", unsafePersistenceCount)
	}

	invalidDocumentID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO indexer_capability_profiles(id,indexer_instance_id,revision,schema_version,lifecycle_state,profile_document,document_sha256,endpoint_config_sha256,created_at)
		SELECT $1,indexer_instance_id,revision+1,1,'draft',$2::jsonb,sha256(convert_to($2::jsonb::text,'UTF8')),endpoint_config_sha256,now()
		FROM indexer_capability_profiles WHERE id=$3`, invalidDocumentID.String(), `{"schema_version":1,"unknown_field":true}`, endpointDraft.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.IndexerCapabilityProfile(ctx, invalidDocumentID); !errors.Is(err, ErrInvalidIndexerCapabilityProfile) {
		t.Fatalf("load accepted a digest-valid but structurally invalid profile: %v", err)
	}
}

func TestIndexerCapabilityActivationGatesCategoryReviewAndProviderSelection(t *testing.T) {
	databaseURL := os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	store := openIndexerCapabilityTestStore(t, ctx, databaseURL)
	defer store.Close()

	integration, err := store.CreateIntegration(ctx, Integration{
		Kind:      "newznab",
		Name:      "Review gate",
		Config:    json.RawMessage(`{"base_url":"https://nzbfinder.ws/api","categories":[2000,5000],"provider_contract_key":"nzbfinder"}`),
		SecretRef: "NEWZNAB_CAPABILITY_REVIEW_KEY",
	})
	if err != nil {
		t.Fatal(err)
	}
	document := activatableCapabilityProfileDocumentWithContract(t, newznab.ProviderContractRef{Key: newznab.ProviderContractNZBFinderV1, Version: 1})
	observedEndpointDigest, err := store.IndexerEndpointConfigurationDigest(ctx, integration.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateIndexerCapabilityProfile(ctx, integration.ID, document, observedEndpointDigest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, integration.ID, profile.ID, "owner", time.Now().UTC()); !errors.Is(err, ErrIndexerCategoryReviewRequired) {
		t.Fatalf("activate before category review: %v", err)
	}
	var stillReviewRequired bool
	if err := store.Pool.QueryRow(ctx, `SELECT (config->'category_selection_review_required')::boolean FROM integration_instances WHERE id=$1`, integration.ID.String()).Scan(&stillReviewRequired); err != nil || !stillReviewRequired {
		t.Fatalf("activation mutated category review state: required=%v err=%v", stillReviewRequired, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{category_selection_review_required}','false'::jsonb,true) WHERE id=$1`, integration.ID.String()); err != nil {
		t.Fatal(err)
	}
	generic, err := store.CreateIndexerCapabilityProfile(ctx, integration.ID, activatableCapabilityProfileDocument(t), observedEndpointDigest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, integration.ID, generic.ID, "owner", time.Now().UTC()); err != nil {
		t.Fatalf("activate independently verified generic profile with configured contract: %v", err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, integration.ID, profile.ID, "owner", time.Now().UTC()); err != nil {
		t.Fatalf("activate reviewed contract profile: %v", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{category_selection_review_required}','true'::jsonb,true) WHERE id=$1`, integration.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveIndexerCapabilityProfile(ctx, integration.ID); !errors.Is(err, ErrIndexerCategoryReviewRequired) {
		t.Fatalf("active load ignored renewed category review: %v", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(jsonb_set(config,'{category_selection_review_required}','false'::jsonb,true),'{provider_contract_key}',to_jsonb('drunkenslug'::text),true) WHERE id=$1`, integration.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActiveIndexerCapabilityProfile(ctx, integration.ID); !errors.Is(err, ErrIndexerProviderContractMismatch) {
		t.Fatalf("active load ignored provider contract change: %v", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET config=jsonb_set(config,'{provider_contract_key}',to_jsonb('nzbfinder'::text),true) WHERE id=$1`, integration.ID.String()); err != nil {
		t.Fatal(err)
	}

	mismatchDocument := activatableCapabilityProfileDocumentWithContract(t, newznab.ProviderContractRef{Key: newznab.ProviderContractDrunkenSlugV1, Version: 1})
	mismatch, err := store.CreateIndexerCapabilityProfile(ctx, integration.ID, mismatchDocument, observedEndpointDigest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, integration.ID, mismatch.ID, "owner", time.Now().UTC()); !errors.Is(err, ErrIndexerProviderContractMismatch) {
		t.Fatalf("activate mismatched contract profile: %v", err)
	}

	wrongOriginID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab','Wrong origin',false,false,$2,'NEWZNAB_WRONG_ORIGIN_KEY',now(),now())`, wrongOriginID.String(), `{"base_url":"https://wrong.invalid/api","movie_category_ids":[],"series_category_ids":[],"category_selection_review_required":false,"provider_contract_key":"nzbfinder"}`); err != nil {
		t.Fatal(err)
	}
	wrongOriginEndpointDigest, err := store.IndexerEndpointConfigurationDigest(ctx, wrongOriginID)
	if err != nil {
		t.Fatal(err)
	}
	wrongOriginProfile, err := store.CreateIndexerCapabilityProfile(ctx, wrongOriginID, document, wrongOriginEndpointDigest, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateIndexerCapabilityProfile(ctx, wrongOriginID, wrongOriginProfile.ID, "owner", time.Now().UTC()); !errors.Is(err, ErrIndexerProviderContractMismatch) {
		t.Fatalf("activate matching key on rejected origin: %v", err)
	}
}

func assertCapabilityProfileImmutable(t *testing.T, ctx context.Context, store *Store, profileID uuid.UUID, state string) {
	t.Helper()
	for description, statement := range map[string]string{
		"document":        `UPDATE indexer_capability_profiles SET profile_document='{}'::jsonb WHERE id=$1`,
		"document digest": `UPDATE indexer_capability_profiles SET document_sha256=decode(repeat('00',32),'hex') WHERE id=$1`,
		"endpoint digest": `UPDATE indexer_capability_profiles SET endpoint_config_sha256=decode(repeat('00',32),'hex') WHERE id=$1`,
		"created time":    `UPDATE indexer_capability_profiles SET created_at=created_at+interval '1 second' WHERE id=$1`,
	} {
		if _, err := store.Pool.Exec(ctx, statement, profileID.String()); err == nil {
			t.Fatalf("database changed immutable %s for %s profile", description, state)
		}
	}
	var lifecycleStatement string
	switch state {
	case "active":
		lifecycleStatement = `UPDATE indexer_capability_profiles SET activated_at=activated_at+interval '1 second' WHERE id=$1`
	case "superseded":
		lifecycleStatement = `UPDATE indexer_capability_profiles SET superseded_at=superseded_at+interval '1 second' WHERE id=$1`
	case "invalidated":
		lifecycleStatement = `UPDATE indexer_capability_profiles SET invalidated_at=invalidated_at+interval '1 second' WHERE id=$1`
	case "draft":
		lifecycleStatement = `UPDATE indexer_capability_profiles SET activated_at=now() WHERE id=$1`
	default:
		t.Fatalf("unexpected lifecycle state %q", state)
	}
	if _, err := store.Pool.Exec(ctx, lifecycleStatement, profileID.String()); err == nil {
		t.Fatalf("database changed lifecycle evidence for %s profile", state)
	}
	if _, err := store.Pool.Exec(ctx, `DELETE FROM indexer_capability_profiles WHERE id=$1`, profileID.String()); err == nil {
		t.Fatalf("database deleted %s profile", state)
	}
}

func openIndexerCapabilityTestStore(t *testing.T, ctx context.Context, databaseURL string) *Store {
	t.Helper()
	schema := "mm_indexer_capability_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		admin.Close()
	})
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
	if err := Migrate(ctx, store.Pool); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}

func activatableCapabilityProfileDocument(t *testing.T) json.RawMessage {
	t.Helper()
	return activatableCapabilityProfileDocumentWithContract(t, newznab.ProviderContractRef{})
}

func activatableCapabilityProfileDocumentWithContract(t *testing.T, contract newznab.ProviderContractRef) json.RawMessage {
	t.Helper()
	profile := newznab.CapabilityProfile{
		SchemaVersion: newznab.CapabilityProfileSchemaVersion,
		SearchRecipes: []newznab.SearchRecipe{{
			Key:   "movie-imdb",
			Scope: newznab.SearchScopeMovie,
			Mode:  newznab.SearchModeMovie,
			Query: newznab.QuerySpec{Style: newznab.QueryTitle},
			Bindings: []newznab.WireBinding{{
				Parameter: newznab.WireIMDbID,
				Source:    newznab.BindingIMDbNumeric,
			}},
			Extended:      true,
			Categories:    newznab.CategorySpec{Set: newznab.CategorySelectedMovie},
			VerifiedLimit: 50,
			Role:          newznab.RecipePrimary,
			Priority:      1,
			Verification: newznab.RecipeVerification{
				State:        newznab.VerificationVerified,
				EvidenceKeys: []string{"movie-search"},
			},
		}},
		Operations:  []newznab.OperationCapability{},
		MappingRefs: []newznab.MappingRef{{Key: newznab.MappingNewznabSearchV1, Version: 1}},
		Evidence: []newznab.CommissionEvidence{{
			Key:                    "movie-search",
			Kind:                   "commissioning",
			RecipeKey:              "movie-imdb",
			ObservedAt:             time.Unix(1, 0).UTC(),
			BodySHA256:             strings.Repeat("a", 64),
			SafeOutcome:            "success",
			RawResultCount:         1,
			PlausibleIdentityCount: 1,
		}},
	}
	if contract.Key != "" {
		profile.ProviderContract = &contract
	}
	if err := newznab.ValidateCapabilityProfileForActivation(profile); err != nil {
		t.Fatalf("test profile: %v", err)
	}
	body, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
