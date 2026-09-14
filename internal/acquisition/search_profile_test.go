package acquisition

import (
	"errors"
	"reflect"
	"testing"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
)

// TestMovieCapabilityProfilePlansExactIMDbRequest proves the documented
// profile document is activatable and that the pure planner emits exactly the
// profile's bounded request rather than the legacy dialect.
func TestMovieCapabilityProfilePlansExactIMDbRequest(t *testing.T) {
	profile, err := newznab.DecodeCapabilityProfile(movieCapabilityProfileDocument(t))
	if err != nil {
		t.Fatalf("decode activatable profile: %v", err)
	}
	year := uint16(2019)
	input := SearchPlanInput{Media: catalog.MediaItem{
		Kind:  catalog.KindMovie,
		Title: "Klaus",
		Year:  &year,
		Identities: []catalog.ExternalIdentity{
			{Namespace: "imdb", Value: "tt1234567", State: catalog.IdentityVerified},
			{Namespace: "tmdb", Value: "508965", State: catalog.IdentityVerified},
		},
	}}
	plan, err := PlanSearch(profile, input, newznab.NewznabCategorySelection{Movie: []int{2000}})
	if err != nil {
		t.Fatalf("plan profile search: %v", err)
	}
	want := newznab.SearchRequest{
		Mode:       "movie",
		Params:     []newznab.Param{{Name: "q", Value: "Klaus"}, {Name: "imdbid", Value: "1234567"}},
		Categories: []int{2000},
		Limit:      50,
		Extended:   true,
	}
	if !reflect.DeepEqual(plan.Primary.Request, want) {
		t.Fatalf("profile request=%#v, want %#v", plan.Primary.Request, want)
	}
}

// TestUnusableSearchProfileErrorsAreRecognized pins the closed set of
// repository gates that intentionally fall back to the legacy dialect. Any
// other error must surface rather than silently degrade.
func TestUnusableSearchProfileErrorsAreRecognized(t *testing.T) {
	for _, err := range []error{
		postgres.ErrNotFound,
		postgres.ErrInvalidIndexerCapabilityProfile,
		postgres.ErrIndexerCategoryReviewRequired,
		postgres.ErrIndexerEndpointConfigurationChanged,
		postgres.ErrIndexerProviderContractMismatch,
	} {
		if !isUnusableSearchProfileError(err) {
			t.Fatalf("expected %v to be an unusable-profile condition", err)
		}
	}
	if isUnusableSearchProfileError(errors.New("database unavailable")) {
		t.Fatal("operational error was treated as an unusable-profile condition")
	}
}
