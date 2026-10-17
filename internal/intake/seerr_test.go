package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	seerradapter "github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
)

// Routing provenance must stay out of the payload-hash representation so
// adding it does not invalidate snapshots written before the field existed.
func TestSeerrRequestHashRepresentationOmitsRoutingFields(t *testing.T) {
	zeroed, err := json.Marshal(seerradapter.Request{ID: 42, Status: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"serverId"`, `"profileId"`, `"rootFolder"`} {
		if bytes.Contains(zeroed, []byte(key)) {
			t.Fatalf("request payload hash representation must omit %s so routing provenance does not change existing snapshot hashes", key)
		}
	}
}

func TestSeerrIntakeHashIsOrderStableAndEpisodeSensitive(t *testing.T) {
	request := seerradapter.Request{ID: 42}
	first := postgres.SeerrIntake{
		Identities: []catalog.ExternalIdentity{{Namespace: "tvdb", Value: "2"}, {Namespace: "tmdb", Value: "1"}},
		Seasons:    []postgres.SeerrSeason{{Number: 2, Episodes: []postgres.SeerrEpisode{{Number: 2}, {Number: 1}}}, {Number: 1, Episodes: []postgres.SeerrEpisode{{Number: 1}}}},
	}
	second := postgres.SeerrIntake{
		Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "1"}, {Namespace: "tvdb", Value: "2"}},
		Seasons:    []postgres.SeerrSeason{{Number: 1, Episodes: []postgres.SeerrEpisode{{Number: 1}}}, {Number: 2, Episodes: []postgres.SeerrEpisode{{Number: 1}, {Number: 2}}}},
	}
	if !bytes.Equal(seerrIntakeHash(request, first), seerrIntakeHash(request, second)) {
		t.Fatal("equivalent Seerr snapshots produced different hashes")
	}
	second.Seasons[1].Episodes = append(second.Seasons[1].Episodes, postgres.SeerrEpisode{Number: 3})
	if bytes.Equal(seerrIntakeHash(request, first), seerrIntakeHash(request, second)) {
		t.Fatal("changed exact episode snapshot produced the same hash")
	}
}

// Seerr marks a request Completed on its own once it sees the media available
// (its Plex or *arr scan, or Media Manager's own availability sync). Intake must
// not treat that as a revocation: doing so cancelled waiting work and flagged
// failed work as "seerr_request_revoked" for titles that were in Plex.
func TestSeerrRequestDispositionTreatsCompletedAsAvailableNotRevoked(t *testing.T) {
	cases := []struct {
		status int
		want   seerrRequestAction
	}{
		{seerradapter.RequestStatusPending, seerrRequestWithdrawn},
		{seerradapter.RequestStatusApproved, seerrRequestAdmit},
		{seerradapter.RequestStatusDeclined, seerrRequestWithdrawn},
		{seerradapter.RequestStatusFailed, seerrRequestWithdrawn},
		{seerradapter.RequestStatusCompleted, seerrRequestCompleted},
		{0, seerrRequestWithdrawn},
		{99, seerrRequestWithdrawn},
	}
	for _, test := range cases {
		if got := seerrRequestDisposition(test.status); got != test.want {
			t.Errorf("status %d disposition=%d, want %d", test.status, got, test.want)
		}
	}
}

// fakeSeasonSource answers like Seerr: HTTP 500 for a season TMDB does not
// know, an episode list otherwise.
type fakeSeasonSource struct{ calls []int16 }

func (source *fakeSeasonSource) SeasonMetadata(_ context.Context, _ int64, season int16) (seerradapter.SeasonMetadata, error) {
	source.calls = append(source.calls, season)
	if season > 2 {
		return seerradapter.SeasonMetadata{}, errors.New("Seerr returned HTTP 500")
	}
	return seerradapter.SeasonMetadata{SeasonNumber: season, Episodes: []seerradapter.EpisodeMetadata{{ID: 7, EpisodeNumber: 1, Name: "Pilot", AirDate: "2026-01-01"}}}, nil
}

// Seerr request 368 asked for seasons 1, 3 and 4 of a series TMDB lists with
// seasons 1-2 only. Every reconcile failed on the season 3 lookup and retried
// forever. Unknown seasons must get no snapshot instead of failing the request.
func TestFillSeasonEpisodesSkipsSeasonsTMDBDoesNotList(t *testing.T) {
	var metadata seerradapter.Metadata
	if err := json.Unmarshal([]byte(`{"name":"Show","seasons":[{"seasonNumber":1},{"seasonNumber":2}]}`), &metadata); err != nil {
		t.Fatal(err)
	}
	seasons := []postgres.SeerrSeason{{Number: 1}, {Number: 3}, {Number: 4}}
	source := &fakeSeasonSource{}
	if err := fillSeasonEpisodes(context.Background(), source, 318948, metadata, seasons); err != nil {
		t.Fatalf("fillSeasonEpisodes = %v, want nil", err)
	}
	if len(source.calls) != 1 || source.calls[0] != 1 {
		t.Fatalf("looked up seasons %v, want only [1]", source.calls)
	}
	if len(seasons[0].Episodes) != 1 || len(seasons[1].Episodes) != 0 || len(seasons[2].Episodes) != 0 {
		t.Fatalf("snapshots = %+v, want episodes only for season 1", seasons)
	}

	// Without a season list from Seerr every requested season is looked up, and
	// a real lookup failure is still returned (retried).
	if err := fillSeasonEpisodes(context.Background(), &fakeSeasonSource{}, 318948, seerradapter.Metadata{}, []postgres.SeerrSeason{{Number: 3}}); err == nil {
		t.Fatal("lookup failure without a season list must be returned")
	}
}
