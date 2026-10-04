package releases

import "testing"

func TestConflictingMovieYearUsesReleaseYearAfterSavedTitle(t *testing.T) {
	for _, test := range []struct {
		name, release string
		year          uint16
		titles        []string
		want          bool
	}{
		{"wrong remake", "Fantastic.Four.2015.1080p", 1994, []string{"The Fantastic Four", "Fantastic Four"}, true},
		{"saved alias", "Les.4.Fantastiques.(2005).1080p", 1994, []string{"The Fantastic Four", "Les 4 Fantastiques"}, true},
		{"festival year", "A.Monster.Calls.2017.1080p", 2016, []string{"A Monster Calls"}, false},
		{"prior festival year", "A.Monster.Calls.2015.1080p", 2016, []string{"A Monster Calls"}, false},
		{"no year", "A.Monster.Calls.1080p", 2016, []string{"A Monster Calls"}, false},
		{"year in name", "2001.A.Space.Odyssey.1968.1080p", 1968, []string{"2001: A Space Odyssey"}, false},
		{"longest title", "Blade.Runner.2049.2017.2160p", 2017, []string{"Blade Runner", "Blade Runner 2049"}, false},
		{"Nordic spelling", "Krummerne.Saa.er.det.jul.2006.1080p", 1994, []string{"Krummerne: Så er det jul"}, true},
		{"no title match", "Other.Movie.2015.1080p", 1994, []string{"Fantastic Four"}, false},
		{"no canonical year", "Fantastic.Four.2015.1080p", 0, []string{"Fantastic Four"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ConflictingMovieYear(test.release, test.year, test.titles); got != test.want {
				t.Fatalf("conflict=%v want=%v", got, test.want)
			}
		})
	}
}
