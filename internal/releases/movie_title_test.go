package releases

import "testing"

func TestMovieTitleMatchesOnlyCanonicalYearRelocation(t *testing.T) {
	for _, test := range []struct {
		name, release, title string
		year                 uint16
		want                 bool
	}{
		{"scene event date", "WWE.SummerSlam.Sunday.2026.1080p.WEB", "WWE SummerSlam 2026: Sunday", 2026, true},
		{"canonical date position", "WWE.SummerSlam.2026.Sunday.1080p", "WWE SummerSlam 2026: Sunday", 2026, true},
		{"different part", "WWE.SummerSlam.Saturday.2026.1080p", "WWE SummerSlam 2026: Sunday", 2026, false},
		{"different year", "WWE.SummerSlam.Sunday.2025.1080p", "WWE SummerSlam 2026: Sunday", 2026, false},
		{"no year", "WWE.SummerSlam.Sunday.1080p", "WWE SummerSlam 2026: Sunday", 2026, false},
		{"reordered words", "SummerSlam.WWE.Sunday.2026.1080p", "WWE SummerSlam 2026: Sunday", 2026, false},
		{"year is movie name", "A.Space.Odyssey.2001.1968.1080p", "2001: A Space Odyssey", 1968, false},
		{"unknown canonical year", "WWE.SummerSlam.Sunday.2026.1080p", "WWE SummerSlam 2026: Sunday", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := MovieTitleMatches(test.release, test.title, test.year); got != test.want {
				t.Fatalf("match=%v want=%v", got, test.want)
			}
		})
	}
}
