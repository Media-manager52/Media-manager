package mediainfo

import "testing"

func TestTooShort(t *testing.T) {
	tests := []struct {
		name           string
		seconds        float64
		expectedMinute float64
		kind           LengthKind
		want           bool
	}{
		{"unknown duration is never judged", 0, 95, LengthMovie, false},
		{"negative duration is never judged", -5, 95, LengthMovie, false},
		{"one minute movie is too short", 60, 95, LengthMovie, true},
		{"one minute episode is too short", 60, 45, LengthEpisode, true},
		{"unknown runtime, tiny movie", 5 * 60, 0, LengthMovie, true},
		{"unknown runtime, ordinary movie", 11 * 60, 0, LengthMovie, false},
		{"unknown runtime, four minute movie", 4 * 60, 0, LengthMovie, true},
		{"unknown runtime, tiny episode", 90, 0, LengthEpisode, true},
		{"unknown runtime, episode just over three minutes", 181, 0, LengthEpisode, false},
		{"short subject below the judged floor", 5 * 60, 10, LengthMovie, false},
		{"half of the expected runtime is accepted", 45 * 60, 90, LengthMovie, false},
		{"just below half of the expected runtime", 45*60 - 1, 90, LengthMovie, true},
		{"full episode", 44 * 60, 45, LengthEpisode, false},
		{"cut-off episode", 12 * 60, 45, LengthEpisode, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := TooShort(test.seconds, test.expectedMinute, test.kind); got != test.want {
				t.Fatalf("TooShort(%v, %v, %v) = %v, want %v", test.seconds, test.expectedMinute, test.kind, got, test.want)
			}
		})
	}
}
