package postgres

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
)

func TestQualityProfileCanonicalizesOriginalLanguageRouting(t *testing.T) {
	profile, err := canonicalQualityProfile(QualityProfile{
		MediaKind: catalog.KindMovie,
		Name:      "Danish original",
		Policy:    json.RawMessage(`{"rules":[{"name":"Resolution","fact":"resolution","allowed_values":["1080p"],"kind":"required","unknown":"hold"}],"original_language_routing":["Danish","da-DK","dansk"]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Languages []string `json:"original_language_routing"`
	}
	if err := json.Unmarshal(profile.Policy, &policy); err != nil {
		t.Fatal(err)
	}
	if len(policy.Languages) != 1 || policy.Languages[0] != "da" {
		t.Fatalf("canonical original-language routing=%v, want [da]", policy.Languages)
	}
}

func TestQualityProfileRejectsUnreliableOriginalLanguageRouting(t *testing.T) {
	_, err := canonicalQualityProfile(QualityProfile{
		MediaKind: catalog.KindMovie,
		Name:      "Unreliable route",
		Policy:    json.RawMessage(`{"rules":[{"name":"Resolution","fact":"resolution","allowed_values":["1080p"],"kind":"required","unknown":"hold"}],"original_language_routing":["maybe Danish"]}`),
	})
	if err == nil || !strings.Contains(err.Error(), "unreliable original-language") {
		t.Fatalf("error=%v, want unreliable original-language rejection", err)
	}
}

func TestQualityProfileRejectsOriginalLanguageRoutingAsDefault(t *testing.T) {
	_, err := canonicalQualityProfile(QualityProfile{
		MediaKind: catalog.KindMovie,
		Name:      "Ambiguous fallback",
		Policy:    json.RawMessage(`{"rules":[{"name":"Resolution","fact":"resolution","allowed_values":["1080p"],"kind":"required","unknown":"hold"}],"original_language_routing":["da"]}`),
		Default:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be the default fallback") {
		t.Fatalf("error=%v, want routed default rejection", err)
	}
}
