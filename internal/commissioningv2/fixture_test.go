package commissioningv2

import (
	"encoding/json"
	"strings"
	"testing"
)

func fixtureYear(value uint16) *uint16 { return &value }

func validFixtureDocument() FixtureDocument {
	return FixtureDocument{
		SchemaVersion: DocumentSchemaVersion,
		Movie: &MovieFixture{
			Key:            "cfx_11111111111111111111111111111111",
			CanonicalTitle: "Ærlige Film",
			Year:           fixtureYear(2026),
			Identities: []VerifiedIdentity{
				{Namespace: IdentityIMDb, Value: "tt0111161"},
				{Namespace: IdentityTMDb, Value: "12345"},
			},
			Source: FixtureExplicitOwner,
		},
		CompleteSeason: &CompleteSeasonFixture{
			Key:            "cfx_22222222222222222222222222222222",
			CanonicalTitle: "Reacher",
			Season:         0,
			Year:           fixtureYear(2022),
			Identities: []VerifiedIdentity{
				{Namespace: IdentityTVDb, Value: "414891"},
				{Namespace: IdentityTVMaze, Value: "53655"},
			},
			Source: FixtureExplicitOwner,
		},
	}
}

func TestNormalizeFixtureDocumentIsBoundedAndDeterministic(t *testing.T) {
	t.Parallel()
	input := validFixtureDocument()
	input.Movie.CanonicalTitle = "  Ærlige Film  "
	input.Movie.Identities[0], input.Movie.Identities[1] = input.Movie.Identities[1], input.Movie.Identities[0]
	first, err := NormalizeFixtureDocument(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NormalizeFixtureDocument(first)
	if err != nil {
		t.Fatal(err)
	}
	if first.Movie.CanonicalTitle != "Ærlige Film" || first.Movie.Identities[0].Namespace != IdentityIMDb {
		t.Fatalf("fixture not canonicalized: %#v", first.Movie)
	}
	firstDigest, _ := FixtureDocumentDigest(first)
	secondDigest, _ := FixtureDocumentDigest(second)
	if firstDigest != secondDigest {
		t.Fatal("canonical fixture digest is not stable")
	}
	input.Movie.CanonicalTitle = "changed"
	if first.Movie.CanonicalTitle != "Ærlige Film" {
		t.Fatal("normalized fixture aliases input")
	}
}

func TestFixtureYearAndSeasonBounds(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		year  uint16
		valid bool
	}{{"year 1799", 1799, false}, {"year 1800", 1800, true}, {"year 9999", 9999, true}, {"year 10000", 10000, false}, {"maximum year", ^uint16(0), false}} {
		t.Run(test.name, func(t *testing.T) {
			document := validFixtureDocument()
			document.Movie.Year = fixtureYear(test.year)
			if err := ValidateFixtureDocument(mustNormalizeFixtureForTest(document)); (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
	for _, test := range []struct {
		season uint16
		valid  bool
	}{{0, true}, {999, true}, {1000, false}, {^uint16(0), false}} {
		document := validFixtureDocument()
		document.CompleteSeason.Season = test.season
		_, err := NormalizeFixtureDocument(document)
		if (err == nil) != test.valid {
			t.Fatalf("season=%d valid=%v err=%v", test.season, test.valid, err)
		}
	}
}

func TestFixtureStrictDecodeAndTextSafety(t *testing.T) {
	t.Parallel()
	document, err := NormalizeFixtureDocument(validFixtureDocument())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeFixtureDocument(raw); err != nil {
		t.Fatal(err)
	}
	invalidTitleUTF8 := append([]byte(`{"schema_version":1,"movie":{"key":"cfx_11111111111111111111111111111111","canonical_title":"`), 0xff)
	invalidTitleUTF8 = append(invalidTitleUTF8, []byte(`","source":"explicit_owner_assertion"}}`)...)
	for _, invalid := range [][]byte{
		append(raw, []byte(` {}`)...),
		[]byte(`{"schema_version":1,"unknown":true}`),
		invalidTitleUTF8,
	} {
		if _, err := DecodeFixtureDocument(invalid); err == nil {
			t.Fatalf("accepted invalid fixture document %q", invalid)
		}
	}
	for _, title := range []string{"", strings.Repeat("x", maximumFixtureTitleBytes+1), "bad\nheader", "bad\x00title"} {
		candidate := validFixtureDocument()
		candidate.Movie.CanonicalTitle = title
		if _, err := NormalizeFixtureDocument(candidate); err == nil {
			t.Fatalf("accepted unsafe title %q", title)
		}
	}
	for _, title := range []string{
		"https://provider.invalid/title",
		"https%3A%2F%2Fprovider.invalid%2Ftitle",
		"apikey=synthetic",
		"Authorization: Bearer synthetic",
		"user:pass@provider.invalid/title",
	} {
		candidate := validFixtureDocument()
		candidate.Movie.CanonicalTitle = title
		if _, err := NormalizeFixtureDocument(candidate); err == nil {
			t.Fatalf("accepted structurally unsafe title %q", title)
		}
	}
	for _, title := range []string{"Movies 100%", "C++: The Movie", "ÆØÅ – Director's Cut"} {
		candidate := validFixtureDocument()
		candidate.Movie.CanonicalTitle = title
		if _, err := NormalizeFixtureDocument(candidate); err != nil {
			t.Fatalf("rejected safe title %q: %v", title, err)
		}
	}
}

func TestFixtureIdentitiesAreClosedAndCanonical(t *testing.T) {
	t.Parallel()
	for _, identities := range [][]VerifiedIdentity{
		{{Namespace: "unknown", Value: "1"}},
		{{Namespace: IdentityIMDb, Value: "1234567"}},
		{{Namespace: IdentityTMDb, Value: "0"}},
		{{Namespace: IdentityTMDb, Value: "01"}},
		{{Namespace: IdentityTMDb, Value: "1"}, {Namespace: IdentityTMDb, Value: "2"}},
	} {
		document := validFixtureDocument()
		document.Movie.Identities = identities
		if _, err := NormalizeFixtureDocument(document); err == nil {
			t.Fatalf("accepted identities %#v", identities)
		}
	}
}

func TestNewFixtureKeyUsesLocalClosedShape(t *testing.T) {
	t.Parallel()
	key, err := NewFixtureKey()
	if err != nil {
		t.Fatal(err)
	}
	if !fixtureKeyPattern.MatchString(key) {
		t.Fatalf("fixture key %q has invalid shape", key)
	}
}

func mustNormalizeFixtureForTest(document FixtureDocument) FixtureDocument {
	// This helper intentionally returns the original value when normalization
	// fails so the caller can exercise ValidateFixtureDocument's direct path.
	normalized, err := NormalizeFixtureDocument(document)
	if err != nil {
		return document
	}
	return normalized
}
