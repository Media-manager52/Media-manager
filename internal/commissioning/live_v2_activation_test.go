package commissioning

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/postgres"
	"uuid"
)

// TestActivateProfileIsOwnerGatedRevisionBoundAndOffline proves the
// Milestone-E service boundary: activation binds the exact reviewed profile
// revision, refuses a stale revision, replays idempotently, and never resolves
// a credential, builds a client, or performs a transmission.
func TestActivateProfileIsOwnerGatedRevisionBoundAndOffline(t *testing.T) {
	store, ctx := draftServiceStore(t)
	transport := &draftRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	now := time.Now().UTC().Truncate(time.Microsecond)
	indexerID := uuid.NewV7()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,'newznab',$2,false,false,$3,'ACTIVATION_SERVICE_KEY',$4,$4)`, indexerID.String(), "Activation service "+indexerID.String(), `{"base_url":"https://activation-service.invalid/api","movie_category_ids":[2000],"series_category_ids":[5000],"category_selection_review_required":false}`, now); err != nil {
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
	if err != nil || finalized.GeneratedProfileID == nil {
		t.Fatalf("finalize draft=%#v err=%v", finalized, err)
	}
	profile, err := service.Draft(ctx, indexerID, run.ID)
	if err != nil || profile.LifecycleState != "draft" {
		t.Fatalf("draft=%#v err=%v", profile, err)
	}

	if _, err := service.ActivateProfile(ctx, indexerID, run.ID, 0); !errors.Is(err, postgres.ErrInvalidIndexerCapabilityProfile) {
		t.Fatalf("zero revision error=%v", err)
	}
	if _, err := service.ActivateProfile(ctx, indexerID, run.ID, profile.Revision+1); !errors.Is(err, postgres.ErrConcurrentModification) {
		t.Fatalf("stale revision error=%v", err)
	}
	if _, err := service.ActivateProfile(ctx, uuid.NewV7(), run.ID, profile.Revision); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("cross-indexer activation error=%v", err)
	}
	if _, err := service.ActivateProfile(ctx, indexerID, uuid.NewV7(), profile.Revision); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("unknown run activation error=%v", err)
	}

	active, err := service.ActivateProfile(ctx, indexerID, run.ID, profile.Revision)
	if err != nil || active.ID != profile.ID || active.LifecycleState != "active" || active.ActivatedAt == nil {
		t.Fatalf("activation=%#v err=%v", active, err)
	}
	replayed, err := service.ActivateProfile(ctx, indexerID, run.ID, profile.Revision)
	if err != nil || replayed.ID != profile.ID || replayed.LifecycleState != "active" {
		t.Fatalf("idempotent replay=%#v err=%v", replayed, err)
	}
	if resolveCalls.Load() != 0 || clientCalls.Load() != 0 || transport.calls.Load() != 0 {
		t.Fatalf("activation performed external work: resolver=%d client=%d transport=%d", resolveCalls.Load(), clientCalls.Load(), transport.calls.Load())
	}
}
