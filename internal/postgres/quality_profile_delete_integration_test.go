package postgres

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestDeleteQualityProfileOnlyWhenUnused proves that deleting a profile can
// never change what an existing title requires: the default profile and a
// profile any workflow uses are refused, and an unused one is removed.
func TestDeleteQualityProfileOnlyWhenUnused(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	ctx, store := fixture.ctx, fixture.store
	now := time.Now().UTC()

	spare, err := store.CreateQualityProfile(ctx, QualityProfile{MediaKind: catalog.KindMovie, Name: "Spare movie profile", PolicyVersion: 1, Policy: fixture.profile.Policy, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fixture.movieWorkflow(t, "Profile In Use", "590001")

	if err := store.DeleteQualityProfile(ctx, fixture.profile.ID, "owner", now); !errors.Is(err, ErrQualityProfileIsDefault) && !errors.Is(err, ErrQualityProfileInUse) {
		t.Fatalf("deleting the fixture profile: err=%v, want default or in-use refusal", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE quality_profiles SET is_default=false WHERE id=$1`, fixture.profile.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteQualityProfile(ctx, fixture.profile.ID, "owner", now); !errors.Is(err, ErrQualityProfileInUse) {
		t.Fatalf("deleting a profile a workflow uses: err=%v, want ErrQualityProfileInUse", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE quality_profiles SET is_default=true WHERE id=$1`, spare.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteQualityProfile(ctx, spare.ID, "owner", now); !errors.Is(err, ErrQualityProfileIsDefault) {
		t.Fatalf("deleting the default profile: err=%v, want ErrQualityProfileIsDefault", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE quality_profiles SET is_default=false WHERE id=$1`, spare.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteQualityProfile(ctx, spare.ID, "owner", now); err != nil {
		t.Fatalf("deleting an unused profile: %v", err)
	}
	var remaining, audited int
	if err := store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM quality_profiles WHERE id IN ($1,$2)),(SELECT count(*) FROM audit_events WHERE action='quality_profile.deleted' AND aggregate_id=$2)`, fixture.profile.ID.String(), spare.ID.String()).Scan(&remaining, &audited); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 || audited != 1 {
		t.Fatalf("remaining=%d audited=%d, want the used profile kept and one audit event", remaining, audited)
	}
	if err := store.DeleteQualityProfile(ctx, spare.ID, "owner", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting twice: err=%v, want ErrNotFound", err)
	}
	if err := store.DeleteQualityProfile(ctx, uuid.NewV7(), "owner", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting an unknown profile: err=%v, want ErrNotFound", err)
	}
}
