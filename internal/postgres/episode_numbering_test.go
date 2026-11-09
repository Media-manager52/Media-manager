package postgres

import (
	"testing"

	"github.com/media-manager/media-manager/internal/packs"
)

func TestAnnualSeasonYearUniqueIsPerSeason(t *testing.T) {
	dated := func(season uint16, date string) []packs.ExpectedEpisode {
		return []packs.ExpectedEpisode{{Key: packs.EpisodeKey{Season: season, Episode: 1}, AirDate: date}}
	}
	snapshots := map[uint16][]packs.ExpectedEpisode{
		1: dated(1, "2020-03-01"),
		2: dated(2, "2020-10-01"),
		3: dated(3, "2021-03-01"),
		4: {{Key: packs.EpisodeKey{Season: 4, Episode: 1}}},
	}
	for season, want := range map[uint16]bool{1: false, 2: false, 3: true, 4: false, 9: false} {
		if got := annualSeasonYearUnique(snapshots, season); got != want {
			t.Errorf("season %d unique=%v, want %v", season, got, want)
		}
	}
}
