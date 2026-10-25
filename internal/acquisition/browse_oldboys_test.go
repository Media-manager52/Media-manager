package acquisition

import (
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
)

func TestOldBoysBrowseRequestUsesDanishSceneScope(t *testing.T) {
	series := oldBoysBrowseRequest(BrowseInput{Kind: catalog.KindSeries, Title: "SEAL Team", Year: 2017, Season: 1, Episode: 1})
	if series.Name != "SEAL Team" || series.Category != 2 || series.Page != 1 {
		t.Fatalf("series request = %+v", series)
	}
	movie := oldBoysBrowseRequest(BrowseInput{Kind: catalog.KindMovie, Title: "The Matrix", Year: 1999})
	if movie.Name != "The Matrix" || movie.Category != 1 || movie.Page != 1 {
		t.Fatalf("movie request = %+v", movie)
	}
}

func TestOldBoysBrowseMatchesExactEpisode(t *testing.T) {
	media := catalog.MediaItem{Title: "SEAL Team"}
	in := BrowseInput{Kind: catalog.KindSeries, Season: 1, Episode: 1}
	observation := releases.Observation{ParseVersion: "oldboys-v1", Title: "SEAL Team S01E01 NORDiC 720p WEB-DL DANiSH"}
	if !oldBoysBrowseMatches(observation, media, in) {
		t.Fatal("matching OldBoys episode was rejected")
	}
	observation.Title = "SEAL Team S01 NORDiC 720p WEB-DL"
	if !oldBoysBrowseMatches(observation, media, in) {
		t.Fatal("matching season pack was rejected")
	}
	observation.Title = "SEAL Team S02 NORDiC 720p WEB-DL"
	if oldBoysBrowseMatches(observation, media, in) {
		t.Fatal("wrong season pack was accepted")
	}
	observation.Title = "SEAL Team S01E02 NORDiC 720p WEB-DL DANiSH"
	if oldBoysBrowseMatches(observation, media, in) {
		t.Fatal("wrong episode was accepted")
	}
	observation.Title = "Another Show S01E01 NORDiC 720p WEB-DL DANiSH"
	if oldBoysBrowseMatches(observation, media, in) {
		t.Fatal("unrelated title was accepted")
	}
}
