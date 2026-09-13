package commissioning

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/commissioningv2"
	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

type draftRoundTripper struct {
	calls atomic.Int32
}

func (transport *draftRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, http.ErrHandlerTimeout
}

// draftServiceStore opens an isolated schema in the disposable test database.
func draftServiceStore(t *testing.T) (*postgres.Store, context.Context) {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("MEDIA_MANAGER_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("MEDIA_MANAGER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	schema := "mm_commissioning_draft_service_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	store, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, store.Pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		store.Close()
		cleanup, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		admin.Close()
	})
	return store, ctx
}

func draftServiceFixtures() commissioningv2.FixtureDocument {
	movieYear, seasonYear := uint16(2026), uint16(2022)
	return commissioningv2.FixtureDocument{
		SchemaVersion: commissioningv2.DocumentSchemaVersion,
		Movie: &commissioningv2.MovieFixture{
			Key: "cfx_00000000000000000000000000000001", CanonicalTitle: "Service Movie", Year: &movieYear,
			Identities: []commissioningv2.VerifiedIdentity{{Namespace: commissioningv2.IdentityIMDb, Value: "tt1234567"}},
			Source:     commissioningv2.FixtureExplicitOwner,
		},
		CompleteSeason: &commissioningv2.CompleteSeasonFixture{
			Key: "cfx_00000000000000000000000000000002", CanonicalTitle: "Service Series", Season: 2, Year: &seasonYear,
			Identities: []commissioningv2.VerifiedIdentity{{Namespace: commissioningv2.IdentityTVDb, Value: "456"}},
			Source:     commissioningv2.FixtureExplicitOwner,
		},
	}
}

func draftServiceCAPSEvidence(observedAt time.Time) commissioningv2.StepEvidenceDocument {
	observedAt = observedAt.UTC().Truncate(time.Microsecond)
	bodyDigest := commissioningv2.SumSHA256([]byte("service CAPS response"))
	caps := newznab.CAPSRecord{
		BodySHA256: bodyDigest.String(), ObservedAt: observedAt, DefaultLimit: 20, MaximumLimit: 100,
		Categories: []newznab.Category{{ID: 2000, Name: "Movies"}, {ID: 5000, Name: "TV"}},
		ModeObservations: []newznab.CAPSModeObservation{
			{Mode: newznab.SearchModeGeneral, ElementState: newznab.CAPSElementPresentAvailable, AdvertisedParameterNames: []string{"q"}, RecognizedParameters: []newznab.WireParameter{newznab.WireQuery}},
			{Mode: newznab.SearchModeMovie, ElementState: newznab.CAPSElementPresentAvailable, AdvertisedParameterNames: []string{"q", "imdbid", "extended"}, RecognizedParameters: []newznab.WireParameter{newznab.WireQuery, newznab.WireIMDbID, newznab.WireExtended}},
			{Mode: newznab.SearchModeTV, ElementState: newznab.CAPSElementPresentAvailable, AdvertisedParameterNames: []string{"q", "tvdbid", "season"}, RecognizedParameters: []newznab.WireParameter{newznab.WireQuery, newznab.WireTVDbID, newznab.WireSeason}},
		},
	}
	return commissioningv2.StepEvidenceDocument{
		SchemaVersion: commissioningv2.DocumentSchemaVersion,
		Key:           "evidence." + strings.Repeat("9", 32),
		Kind:          newznab.EvidenceKindControlledProbe,
		Target:        newznab.EvidenceTarget{Kind: newznab.EvidenceTargetCAPS},
		ObservedAt:    observedAt,
		SafeOutcome:   commissioningv2.OutcomeCAPSObserved,
		BodySHA256:    bodyDigest,
		CAPS:          &caps,
	}
}

// draftServiceCompletedRun drives a real repository run to the point where
// draft finalization is legal. It uses only the offline durable transitions,
// so it is a faithful stand-in for a run whose CAPS and search probes were
// already accepted.
func draftServiceCompletedRun(t *testing.T, ctx context.Context, store *postgres.Store, indexerID uuid.UUID, now time.Time) postgres.IndexerCommissioningRun {
	t.Helper()
	digestBytes, err := store.IndexerEndpointConfigurationDigest(ctx, indexerID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := commissioningv2.SHA256DigestFromBytes(digestBytes)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := draftServiceFixtures()
	capsPlan, err := commissioningv2.BuildCAPSPlan(commissioningv2.CAPSPlanInput{ConfigurationSHA256: digest, Fixtures: fixtures, ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateIndexerCommissioningRun(ctx, indexerID, fixtures, capsPlan, now)
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.AuthorizeIndexerCommissioningCAPS(ctx, indexerID, run.ID, run.Revision, "owner", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	capsStep := run.Steps[0]
	leaseUntil := now.Add(time.Hour)
	if capsStep, err = store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, capsStep.ID, commissioningv2.StepPlanned, postgres.IndexerCommissioningStepTransition{State: commissioningv2.StepIntentCommitted, LeaseOwner: "worker.test", LeaseUntil: &leaseUntil}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if capsStep, err = store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, capsStep.ID, commissioningv2.StepIntentCommitted, postgres.IndexerCommissioningStepTransition{State: commissioningv2.StepTransmitting}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	capsEvidence := draftServiceCAPSEvidence(capsStep.TransmittedAt.Add(time.Second))
	zero := uint16(0)
	if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, capsStep.ID, commissioningv2.StepTransmitting, postgres.IndexerCommissioningStepTransition{
		State:                  commissioningv2.StepCompleted,
		SafeOutcome:            commissioningv2.OutcomeCAPSObserved,
		RawResultCount:         &zero,
		PlausibleIdentityCount: &zero,
		RawBodySHA256:          capsEvidence.BodySHA256.Bytes(),
		Evidence:               &capsEvidence,
	}, capsEvidence.ObservedAt); err != nil {
		t.Fatal(err)
	}
	run, err = store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.TransitionIndexerCommissioningRun(ctx, indexerID, run.ID, run.Revision, commissioningv2.RunAwaitingCategoryReview, "", now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	categories := newznab.NewznabCategorySelection{Movie: []int{2000}, Series: []int{5000}}
	searchPlan, err := commissioningv2.BuildSearchPlan(commissioningv2.SearchPlanInput{
		ConfigurationSHA256: digest, Fixtures: fixtures, CAPSEvidence: capsEvidence, Categories: categories,
		ProfileSchemaVersion: newznab.CapabilityProfileSchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.SealIndexerCommissioningSearchPlan(ctx, indexerID, run.ID, run.Revision, categories, searchPlan, now.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.AuthorizeIndexerCommissioningSearches(ctx, indexerID, run.ID, run.Revision, "owner", now.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index < len(run.Steps); index++ {
		step := run.Steps[index]
		lease := now.Add(time.Hour)
		var err error
		if step, err = store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, step.ID, commissioningv2.StepPlanned, postgres.IndexerCommissioningStepTransition{State: commissioningv2.StepIntentCommitted, LeaseOwner: "worker.test", LeaseUntil: &lease}, now.Add(time.Duration(9+index*3)*time.Second)); err != nil {
			t.Fatal(err)
		}
		if step, err = store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, step.ID, commissioningv2.StepIntentCommitted, postgres.IndexerCommissioningStepTransition{State: commissioningv2.StepTransmitting}, now.Add(time.Duration(10+index*3)*time.Second)); err != nil {
			t.Fatal(err)
		}
		observedAt := step.TransmittedAt.UTC().Add(time.Second).Truncate(time.Microsecond)
		key := "evidence." + strings.Repeat(string("0123456789abcdef"[index%16]), 32)
		proof := commissioningv2.SearchProof{
			SchemaVersion: commissioningv2.DocumentSchemaVersion, Scope: string(step.Scope),
			ProviderOutcome: "none", IdentityMatch: "observed", TitleMatch: "exact",
			YearMatch: "exact", SeasonMatch: "not_required", CompletePackBasis: "none",
			ValidItemCount: 1, RawResultCount: 1, PlausibleIdentityCount: 1,
		}
		if step.Scope == newznab.SearchScopeCompleteSeason {
			proof.YearMatch = "not_required"
			proof.SeasonMatch = "exact"
			proof.SeasonRequested = 2
			proof.CompletePackBasis = "provider_field"
		}
		one := uint16(1)
		evidence := commissioningv2.StepEvidenceDocument{
			SchemaVersion: commissioningv2.DocumentSchemaVersion, Key: key, Kind: newznab.EvidenceKindControlledProbe,
			Target: step.Target, ObservedAt: observedAt, SafeOutcome: commissioningv2.OutcomeRecipeVerified,
			RawResultCount: one, PlausibleIdentityCount: one, BodySHA256: commissioningv2.SumSHA256([]byte(key)), Search: &proof,
		}
		if _, err := store.TransitionIndexerCommissioningStep(ctx, indexerID, run.ID, step.ID, commissioningv2.StepTransmitting, postgres.IndexerCommissioningStepTransition{
			State:                  commissioningv2.StepCompleted,
			SafeOutcome:            commissioningv2.OutcomeRecipeVerified,
			RawResultCount:         &one,
			PlausibleIdentityCount: &one,
			RawBodySHA256:          evidence.BodySHA256.Bytes(),
			Evidence:               &evidence,
		}, observedAt); err != nil {
			t.Fatal(err)
		}
	}
	run, err = store.IndexerCommissioningRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != commissioningv2.RunSearchesAuthorized {
		t.Fatalf("run state=%s", run.State)
	}
	return run
}

// TestFinalizeDraftPerformsZeroTransmissions proves that draft generation,
// replay, and failure never touch a credential, client factory, or transport.
func TestFinalizeDraftPerformsZeroTransmissions(t *testing.T) {
	store, ctx := draftServiceStore(t)
	// Install a counting default transport for the duration of the test so
	// any accidental generic HTTP client construction is observable.
	transport := &draftRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab',$2,false,false,$3,'DRAFT_SERVICE_KEY',$4,$4)`, indexerID.String(), "Draft service "+indexerID.String(), `{"base_url":"https://draft-service.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000],"category_selection_review_required":false}`, now); err != nil {
		t.Fatal(err)
	}
	run := draftServiceCompletedRun(t, ctx, store, indexerID, now)

	var resolveCalls atomic.Int32
	var clientCalls atomic.Int32
	service := &LiveCAPSService{
		Store: store,
		ResolveSecret: func(string) (string, error) {
			resolveCalls.Add(1)
			return "", nil
		},
		ClientFactory: func(postgres.Integration, liveNewznabConfig, string) (*newznab.Client, newznab.CommissioningDestination, error) {
			clientCalls.Add(1)
			return nil, newznab.CommissioningDestination{}, nil
		},
		Now: func() time.Time { return now.Add(time.Minute) },
	}
	finalized, err := service.FinalizeDraft(ctx, indexerID, run.ID, run.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if finalized.GeneratedProfileID == nil {
		t.Fatal("draft was not linked")
	}
	// Replay the exact request: no new credential, client, or transport work.
	if _, err := service.FinalizeDraft(ctx, indexerID, run.ID, run.Revision); err != nil {
		t.Fatal(err)
	}
	// A failing finalization must also stay offline.
	if _, err := service.FinalizeDraft(ctx, indexerID, uuid.NewV7(), 1); err == nil {
		t.Fatal("expected a not-found finalization to fail")
	}
	if _, err := service.Draft(ctx, indexerID, run.ID); err != nil {
		t.Fatal(err)
	}
	if resolveCalls.Load() != 0 || clientCalls.Load() != 0 || transport.calls.Load() != 0 {
		t.Fatalf("draft path performed external work: resolver=%d client=%d transport=%d", resolveCalls.Load(), clientCalls.Load(), transport.calls.Load())
	}
}
