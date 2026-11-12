package languages

import "testing"

func TestCanonicalDanishVariants(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"da", "dan", "Danish", "dansk", "da-DK", "DA_dk", "dk", "DK"} {
		if canonical, ok := Canonical(value); !ok || canonical != "da" {
			t.Fatalf("Canonical(%q)=(%q,%v), want da,true", value, canonical, ok)
		}
	}
	if canonical, ok := Canonical("commentary"); ok || canonical != "" {
		t.Fatalf("unreliable language accepted as %q", canonical)
	}
}

func TestCanonicalMapsThreeLetterTrackCodes(t *testing.T) {
	for raw, want := range map[string]string{"eng": "en", "ENG": "en", "swe": "sv", "nor": "no", "nob": "no", "ger": "de", "deu": "de", "fre": "fr", "en": "en", "sv": "sv", "dan": "da"} {
		if got, ok := Canonical(raw); !ok || got != want {
			t.Errorf("Canonical(%q) = %q, %v; want %q", raw, got, ok, want)
		}
	}
}
