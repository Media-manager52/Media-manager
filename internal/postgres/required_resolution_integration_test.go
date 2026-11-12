package postgres

import (
	"slices"
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
)

// TestRequiredResolutionsForJobReadsTheRoundAndTheLiveProfile proves the
// output check sees the round's required resolution rule, and that a rule the
// live profile adds after the round started applies too.
func TestRequiredResolutionsForJobReadsTheRoundAndTheLiveProfile(t *testing.T) {
	fixture := newPlexPublicationFixture(t, catalog.KindMovie)
	_, workflowID := fixture.movieWorkflow(t, "Required Resolution", "770015")
	job, _, _ := fixture.seedVerifiedMovieOutput(t, workflowID, 1, "required-resolution")
	required, err := fixture.store.RequiredResolutionsForJob(fixture.ctx, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(required) != 1 || !slices.Equal(required[0], []string{"1080p"}) {
		t.Fatalf("required=%v, want the round's required 1080p rule", required)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE quality_profiles SET policy=jsonb_set(policy,'{rules}',COALESCE(policy->'rules','[]'::jsonb)||'[{"name":"At least 720p","fact":"resolution","kind":"required","unknown":"reject","allowed_values":["720p","1080p","2160p"]}]'::jsonb) WHERE id=$1`, fixture.profile.ID.String()); err != nil {
		t.Fatal(err)
	}
	required, err = fixture.store.RequiredResolutionsForJob(fixture.ctx, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(required) != 2 || !slices.Equal(required[1], []string{"720p", "1080p", "2160p"}) {
		t.Fatalf("required=%v, want the round's rule and the one the live profile added", required)
	}
}
