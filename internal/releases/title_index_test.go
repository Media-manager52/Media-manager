package releases

import (
	"reflect"
	"testing"
)

func TestTitleIndexFindsWhatTitleMatchesAccepts(t *testing.T) {
	t.Parallel()
	titles := []struct {
		title string
		year  uint16
	}{
		{"Forræder", 0},
		{"Law & Order", 0},
		{"The Bear", 0},
		{"The Bear Hunt", 0},
		{"SummerSlam 2026 Sunday", 2026},
		{"Don't Look Up", 2021},
	}
	index := NewTitleIndex()
	for entry, item := range titles {
		index.Add(item.title, item.year, entry)
	}
	releasesNamed := []string{
		"Forraeder.S02E03.DANiSH.1080p.WEB.h264-GRP",
		"Forrader.S02.1080p.WEB",
		"Law.and.Order.S24E01.720p.HDTV",
		"Law &amp;amp; Order S24E01",
		"The.Bear.S03E01.2160p.WEB",
		"The.Bear.Hunt.2024.1080p.BluRay",
		"SummerSlam.Sunday.2026.1080p.WEB",
		"Dont.Look.Up.2021.2160p.NF.WEB-DL",
		"The.Bears.S01E01.1080p",
		"Bear.S03E01.1080p",
		"Unrelated.Movie.2020.1080p",
		"",
	}
	for _, name := range releasesNamed {
		var want []int
		for entry, item := range titles {
			if TitleMatches(name, item.title) || item.year != 0 && MovieTitleMatches(name, item.title, item.year) {
				want = append(want, entry)
			}
		}
		got := index.Lookup(name)
		seen := map[int]bool{}
		for _, entry := range got {
			seen[entry] = true
		}
		if len(seen) != len(got) || len(got) != len(want) {
			t.Fatalf("%q: index=%v, direct=%v", name, got, want)
		}
		for _, entry := range want {
			if !seen[entry] {
				t.Fatalf("%q: index=%v misses %d (direct=%v)", name, got, entry, want)
			}
		}
	}
	if got := index.Lookup("The.Bear.Hunt.2024.1080p.BluRay"); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Fatalf("a longer title did not also return its shorter prefix title: %v", got)
	}
}
