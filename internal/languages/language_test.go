package languages

import "testing"

func TestCanonicalDanishVariants(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"da", "dan", "Danish", "dansk", "da-DK", "DA_dk"} {
		if canonical, ok := Canonical(value); !ok || canonical != "da" {
			t.Fatalf("Canonical(%q)=(%q,%v), want da,true", value, canonical, ok)
		}
	}
	if canonical, ok := Canonical("commentary"); ok || canonical != "" {
		t.Fatalf("unreliable language accepted as %q", canonical)
	}
}
