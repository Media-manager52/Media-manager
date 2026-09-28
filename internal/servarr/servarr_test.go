package servarr

import "testing"

func TestSyntheticIDIsStableAndPositive(t *testing.T) {
	first := SyntheticID("profile", "01a05529-6fd8-7704-b0ec-a6ef3309acb7")
	second := SyntheticID("profile", "01a05529-6fd8-7704-b0ec-a6ef3309acb7")
	if first != second {
		t.Fatalf("synthetic id changed between calls: %d != %d", first, second)
	}
	if first <= 0 {
		t.Fatalf("synthetic id must be positive, got %d", first)
	}
	if other := SyntheticID("movie", "766105"); other <= 0 {
		t.Fatalf("synthetic id must be positive, got %d", other)
	}
	if SyntheticID("movie", "1") == SyntheticID("movie", "2") {
		t.Fatal("different values must not collide in this sample")
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"A Taste of Hunger": "a-taste-of-hunger",
		"Klar Til Krig?":    "klar-til-krig",
		"Børn Film":         "b-rn-film",
		"  Spaced  Out  ":   "spaced-out",
	}
	for input, expected := range cases {
		if actual := Slug(input); actual != expected {
			t.Fatalf("Slug(%q)=%q, want %q", input, actual, expected)
		}
	}
}
