package intake

import (
	"bytes"
	"testing"

	seerradapter "github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
)

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
