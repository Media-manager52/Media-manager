package commissioningv2

import (
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/releases"
)

func observedIdentityFact(name, value string) releases.Fact {
	return releases.Fact{Name: name, Values: []string{value}, State: releases.FactObserved}
}

func searchMatrixFixtures() FixtureDocument {
	movieYear, seasonYear := uint16(2026), uint16(2022)
	return FixtureDocument{
		SchemaVersion: DocumentSchemaVersion,
		Movie: &MovieFixture{
			Key: "cfx_00000000000000000000000000000001", CanonicalTitle: "Commissioning Movie", Year: &movieYear,
			Identities: []VerifiedIdentity{{Namespace: IdentityIMDb, Value: "tt1234567"}, {Namespace: IdentityTMDb, Value: "123"}},
			Source:     FixtureExplicitOwner,
		},
		CompleteSeason: &CompleteSeasonFixture{
			Key: "cfx_00000000000000000000000000000002", CanonicalTitle: "Commissioning Series", Season: 2, Year: &seasonYear,
			Identities: []VerifiedIdentity{{Namespace: IdentityTVDb, Value: "456"}},
			Source:     FixtureExplicitOwner,
		},
	}
}

func searchMatrixSnapshot(scope newznab.SearchScope) LiveAuthorizedStep {
	fixtures := searchMatrixFixtures()
	snapshot := LiveAuthorizedStep{
		IndexerID: "11111111-1111-4111-8111-111111111111",
		Phase:     StepPhaseSearch,
		Scope:     scope,
		Fixtures:  fixtures,
	}
	switch scope {
	case newznab.SearchScopeMovie:
		snapshot.Target = newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.q-imdb.basic"}
		snapshot.FixtureKey = fixtures.Movie.Key
		snapshot.Role = newznab.RecipePrimary
		snapshot.CategoryIDs = []int{2000}
		snapshot.Request = TypedRequest{Operation: newznab.OperationSearch, Mode: newznab.SearchModeMovie, Query: newznab.QuerySpec{Style: newznab.QueryTitle}, Bindings: []newznab.WireBinding{{Parameter: newznab.WireIMDbID, Source: newznab.BindingIMDbNumeric}}, Output: OutputXML, Pagination: PaginationNone, RequestLimit: 20, Role: newznab.RecipePrimary}
	default:
		snapshot.Target = newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.season.q-tvdb-season.basic"}
		snapshot.FixtureKey = fixtures.CompleteSeason.Key
		snapshot.Role = newznab.RecipePrimary
		snapshot.CategoryIDs = []int{5000}
		snapshot.Request = TypedRequest{Operation: newznab.OperationSearch, Mode: newznab.SearchModeTV, Query: newznab.QuerySpec{Style: newznab.QueryTitle}, Bindings: []newznab.WireBinding{{Parameter: newznab.WireTVDbID, Source: newznab.BindingTVDbNumeric}, {Parameter: newznab.WireSeason, Source: newznab.BindingSeasonNumber}}, Output: OutputXML, Pagination: PaginationNone, RequestLimit: 20, Role: newznab.RecipePrimary}
	}
	return snapshot
}

func matrixItem(title string, attrs ...string) string {
	return `<item><title>` + title + `</title>` + strings.Join(attrs, "") + `</item>`
}

func matrixAttr(name, value string) string {
	return `<newznab:attr name="` + name + `" value="` + value + `"/>`
}

func matrixRSS(items ...string) string {
	return `<rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel>` + strings.Join(items, "") + `</channel></rss>`
}

func matrixResult(body string) newznab.CommissioningResult {
	digest := sha256.Sum256([]byte(body))
	return newznab.CommissioningResult{Body: []byte(body), BodySHA256: digest}
}

func classifyMatrix(t *testing.T, snapshot LiveAuthorizedStep, body string) (ClassifiedCompletion, error) {
	t.Helper()
	inputs, err := classifySearchAttemptInputs(snapshot, matrixResult(body), nil, time.Now().UTC(), "evidence.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		return ClassifiedCompletion{}, err
	}
	return ClassifySearchAttempt(inputs)
}

func assertVerdict(t *testing.T, snapshot LiveAuthorizedStep, body string, want SafeOutcome) {
	t.Helper()
	classified, err := classifyMatrix(t, snapshot, body)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if classified.SafeOutcome != want {
		t.Fatalf("outcome=%q want %q evidence=%#v", classified.SafeOutcome, want, classified.Evidence)
	}
	if err := ValidateSearchCompletionEvidence(classified); err != nil {
		t.Fatalf("search completion evidence invalid: %v", err)
	}
}

func TestValidateSearchProofRejectsOpenValues(t *testing.T) {
	base := SearchProof{
		SchemaVersion: DocumentSchemaVersion, Scope: "movie", ProviderOutcome: searchProviderNone,
		IdentityMatch: searchIdentityAbsent, TitleMatch: searchTitleNone, YearMatch: searchYearAbsent,
		SeasonMatch: searchSeasonNotRequired, CompletePackBasis: searchPackNone,
	}
	if err := ValidateSearchProof(base); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}
	tampered := base
	tampered.IdentityMatch = "whatever"
	if err := ValidateSearchProof(tampered); err == nil {
		t.Fatal("open identity-match value accepted")
	}
	tampered = base
	tampered.ProviderOutcome = "guess"
	if err := ValidateSearchProof(tampered); err == nil {
		t.Fatal("open provider outcome accepted")
	}
	tampered = base
	tampered.ConflictCodes = []string{"not_a_real_conflict"}
	if err := ValidateSearchProof(tampered); err == nil {
		t.Fatal("open conflict code accepted")
	}
	tampered = base
	tampered.RawResultCount = defaultProbeLimit + 1
	if err := ValidateSearchProof(tampered); err == nil {
		t.Fatal("over-limit raw count accepted")
	}
}

// TestSearchMatrixMovieIdentityAndYear covers movie identity, exact year,
// year conflict, zero results, and title-only missing evidence.
func TestSearchMatrixMovieIdentityAndYear(t *testing.T) {
	snapshot := searchMatrixSnapshot(newznab.SearchScopeMovie)
	assertVerdict(t, snapshot, matrixRSS(matrixItem("Commissioning Movie 2026", matrixAttr("imdb", "tt1234567"))), OutcomeRecipeVerified)
	assertVerdict(t, snapshot, matrixRSS(), OutcomeNoResults)
	assertVerdict(t, snapshot, matrixRSS(matrixItem("Commissioning Movie 2020", matrixAttr("imdb", "tt1234567"))), OutcomeContradicted)
	assertVerdict(t, snapshot, matrixRSS(matrixItem("Unrelated Release 1999", matrixAttr("imdb", "tt9999999"))), OutcomeNoPlausibleMatch)
	// Title-only with no identity and no year must remain unknown.
	assertVerdict(t, snapshot, matrixRSS(matrixItem("Commissioning Movie")), OutcomeNoPlausibleMatch)
}

// TestSearchMatrixMovieIdentityConflict covers a conflicting trusted identity
// (two different provider IDs on one item), not merely a wrong year.
func TestSearchMatrixMovieIdentityConflict(t *testing.T) {
	snapshot := searchMatrixSnapshot(newznab.SearchScopeMovie)
	body := matrixRSS(matrixItem("Commissioning Movie 2026", matrixAttr("imdb", "tt1234567"), matrixAttr("imdb", "tt9999999")))
	assertVerdict(t, snapshot, body, OutcomeContradicted)
}

// TestSearchMatrixCompleteSeason covers identity, exact season, pack, wrong
// season, conflicting series identity, and missing pack.
func TestSearchMatrixCompleteSeason(t *testing.T) {
	snapshot := searchMatrixSnapshot(newznab.SearchScopeCompleteSeason)
	assertVerdict(t, snapshot, matrixRSS(matrixItem("Commissioning Series S02.1080p", matrixAttr("tvdbid", "456"), matrixAttr("season", "2"))), OutcomeRecipeVerified)
	assertVerdict(t, snapshot, matrixRSS(matrixItem("Commissioning Series S03.1080p", matrixAttr("tvdbid", "456"), matrixAttr("season", "3"))), OutcomeContradicted)
	conflictingIdentity := matrixRSS(matrixItem("Commissioning Series S02.1080p", matrixAttr("tvdbid", "456"), matrixAttr("tvdbid", "999"), matrixAttr("season", "2")))
	assertVerdict(t, snapshot, conflictingIdentity, OutcomeContradicted)
	// Identity + exact season but no pack evidence must not verify.
	assertVerdict(t, snapshot, matrixRSS(matrixItem("Commissioning Series", matrixAttr("tvdbid", "456"), matrixAttr("season", "2"))), OutcomeNoPlausibleMatch)
}

// TestSearchMatrixIndividualEpisodes covers the pack-negative case: plausible
// single episodes must never verify a complete-season probe.
func TestSearchMatrixIndividualEpisodes(t *testing.T) {
	snapshot := searchMatrixSnapshot(newznab.SearchScopeCompleteSeason)
	body := matrixRSS(
		matrixItem("Commissioning Series S02E01.1080p", matrixAttr("tvdbid", "456"), matrixAttr("season", "2")),
		matrixItem("Commissioning Series S02E02.1080p", matrixAttr("tvdbid", "456"), matrixAttr("season", "2")),
	)
	classified, err := classifyMatrix(t, snapshot, body)
	if err != nil {
		t.Fatal(err)
	}
	if classified.SafeOutcome == OutcomeRecipeVerified {
		t.Fatalf("individual episodes verified: %#v", classified.Evidence)
	}
	if classified.SafeOutcome != OutcomeContradicted {
		t.Fatalf("individual episodes outcome=%q, want contradicted", classified.SafeOutcome)
	}
	if classified.Evidence == nil || classified.Evidence.Search == nil || classified.Evidence.Search.IndividualEpisodeCount != 2 {
		t.Fatalf("individual episode accounting wrong: %#v", classified.Evidence)
	}
}

// TestSearchMatrixMissingMetadata covers missing year and missing trusted
// identity where required.
func TestSearchMatrixMissingMetadata(t *testing.T) {
	movie := searchMatrixSnapshot(newznab.SearchScopeMovie)
	// Identity present but no provider year: the identity path verifies.
	assertVerdict(t, movie, matrixRSS(matrixItem("Commissioning Movie", matrixAttr("imdb", "tt1234567"))), OutcomeRecipeVerified)
	// No identity and no year: unknown.
	assertVerdict(t, movie, matrixRSS(matrixItem("Commissioning Movie")), OutcomeNoPlausibleMatch)

	// A QueryNone identity recipe requires the provider identity.
	idSnapshot := movie
	idSnapshot.Target = newznab.EvidenceTarget{Kind: newznab.EvidenceTargetSearchRecipe, RecipeKey: "v2.movie.id-imdb.basic"}
	idSnapshot.Request.Query = newznab.QuerySpec{Style: newznab.QueryNone}
	assertVerdict(t, idSnapshot, matrixRSS(matrixItem("Commissioning Movie 2026")), OutcomeNoPlausibleMatch)
	assertVerdict(t, idSnapshot, matrixRSS(matrixItem("Commissioning Movie 2026", matrixAttr("imdb", "tt1234567"))), OutcomeRecipeVerified)
}

// TestSearchMatrixMixedResults covers a qualifying plausible result among
// unrelated results, a valid complete pack among unrelated results, and a
// conflicting result case.
func TestSearchMatrixMixedResults(t *testing.T) {
	movie := searchMatrixSnapshot(newznab.SearchScopeMovie)
	body := matrixRSS(
		matrixItem("Unrelated Release 1999", matrixAttr("imdb", "tt9999999")),
		matrixItem("Commissioning Movie 2026", matrixAttr("imdb", "tt1234567")),
	)
	classified, err := classifyMatrix(t, movie, body)
	if err != nil {
		t.Fatal(err)
	}
	if classified.SafeOutcome != OutcomeRecipeVerified {
		t.Fatalf("mixed movie outcome=%q want recipe_verified", classified.SafeOutcome)
	}
	if classified.RawResultCount == nil || *classified.RawResultCount != 2 || classified.PlausibleIdentityCount == nil || *classified.PlausibleIdentityCount != 1 {
		t.Fatalf("mixed counts wrong: raw=%v plausible=%v", classified.RawResultCount, classified.PlausibleIdentityCount)
	}

	season := searchMatrixSnapshot(newznab.SearchScopeCompleteSeason)
	seasonBody := matrixRSS(
		matrixItem("Unrelated Show S01", matrixAttr("tvdbid", "111"), matrixAttr("season", "1")),
		matrixItem("Commissioning Series S02.1080p", matrixAttr("tvdbid", "456"), matrixAttr("season", "2")),
	)
	seasonClassified, err := classifyMatrix(t, season, seasonBody)
	if err != nil {
		t.Fatal(err)
	}
	if seasonClassified.SafeOutcome != OutcomeRecipeVerified {
		t.Fatalf("mixed season outcome=%q want recipe_verified", seasonClassified.SafeOutcome)
	}

	conflictingBody := matrixRSS(
		matrixItem("Unrelated Release 1999", matrixAttr("imdb", "tt9999999")),
		matrixItem("Commissioning Movie 2020", matrixAttr("imdb", "tt1234567")),
	)
	assertVerdict(t, movie, conflictingBody, OutcomeContradicted)
}

// TestSearchMatrixMalformedItemIsolation covers one malformed item among valid
// items and proves it cannot contribute to verification.
func TestSearchMatrixMalformedItemIsolation(t *testing.T) {
	snapshot := searchMatrixSnapshot(newznab.SearchScopeMovie)
	malformed := `<item><newznab:attr name="imdb" value="tt0000000"/></item>` // no title -> malformed
	valid := matrixItem("Commissioning Movie 2026", matrixAttr("imdb", "tt1234567"))
	classified, err := classifyMatrix(t, snapshot, matrixRSS(valid, malformed))
	if err != nil {
		t.Fatal(err)
	}
	if classified.SafeOutcome != OutcomeRecipeVerified {
		t.Fatalf("isolated malformed item poisoned verification: %q", classified.SafeOutcome)
	}
	proof := classified.Evidence.Search
	if proof.MalformedItemCount != 1 || proof.ValidItemCount != 1 || proof.RawResultCount != 2 || proof.PlausibleIdentityCount != 1 {
		t.Fatalf("malformed accounting wrong: %#v", proof)
	}

	// A malformed item alone cannot verify; the valid-item set is empty.
	assertVerdict(t, snapshot, matrixRSS(malformed), OutcomeNoPlausibleMatch)
}

// TestSearchMatrixWholeDocumentMalformed covers the whole-document malformed
// path (a parsed but non-RSS envelope fails closed).
func TestSearchMatrixWholeDocumentMalformed(t *testing.T) {
	snapshot := searchMatrixSnapshot(newznab.SearchScopeMovie)
	inputs, err := classifySearchAttemptInputs(snapshot, matrixResult(`<caps><server title="x"/></caps>`), nil, time.Now().UTC(), "evidence.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if inputs.Response != ResponseMalformed || inputs.Evidence != nil {
		t.Fatalf("whole-document malformed inputs=%#v", inputs)
	}
}

// TestSearchMatrixProviderErrors covers deterministic provider protocol proof
// for unsupported and permission_restricted, plus authentication and generic
// provider failures, using only closed provider error evidence.
func TestSearchMatrixProviderErrors(t *testing.T) {
	snapshot := searchMatrixSnapshot(newznab.SearchScopeMovie)
	cases := []struct {
		body     string
		response ResponseVerdict
		outcome  SafeOutcome
		provider string
	}{
		{`<error code="203" description="Function not supported"/>`, ResponseUnsupported, OutcomeUnsupported, searchProviderUnsupported},
		{`<error code="102" description="Insufficient privileges"/>`, ResponsePermissionRestricted, OutcomePermissionRestricted, searchProviderPermissionRestricted},
		{`<error code="100" description="Incorrect credentials"/>`, ResponseAuthenticationFailure, OutcomeAuthenticationFailure, ""},
		{`<error code="999" description="Unexpected"/>`, ResponseMalformed, OutcomeMalformedResponse, ""},
	}
	for _, testCase := range cases {
		inputs, err := classifySearchAttemptInputs(snapshot, matrixResult(testCase.body), nil, time.Now().UTC(), "evidence.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		if err != nil {
			t.Fatalf("%s: %v", testCase.response, err)
		}
		if inputs.Response != testCase.response {
			t.Fatalf("provider body=%q response=%q want %q", testCase.body, inputs.Response, testCase.response)
		}
		classified, err := ClassifySearchAttempt(inputs)
		if err != nil {
			t.Fatalf("%s classify: %v", testCase.response, err)
		}
		if classified.SafeOutcome != testCase.outcome {
			t.Fatalf("body=%q outcome=%q want %q", testCase.body, classified.SafeOutcome, testCase.outcome)
		}
		if testCase.provider != "" {
			if classified.Evidence == nil || classified.Evidence.Search == nil || classified.Evidence.Search.ProviderOutcome != testCase.provider {
				t.Fatalf("body=%q provider outcome not recorded: %#v", testCase.body, classified.Evidence)
			}
		}
		if err := ValidateSearchCompletionEvidence(classified); err != nil {
			t.Fatalf("body=%q evidence invalid: %v", testCase.body, err)
		}
	}
}

// TestLiveTransmissionGuardRejectsSecretEcho proves the shared guard rejects a
// credential echo and accepts a benign body.
func TestLiveTransmissionGuardRejectsSecretEcho(t *testing.T) {
	guard := liveTransmissionGuard{credential: "local-secret"}
	if err := guard.InspectSecret([]byte("<rss>local-secret</rss>"), nil, nil, nil); err == nil {
		t.Fatal("secret echo accepted")
	} else {
		var coded interface{ SafeCode() string }
		if !errors.As(err, &coded) || coded.SafeCode() != "commissioning_secret_echo" {
			t.Fatalf("secret-echo code=%v err=%v", coded, err)
		}
	}
	if err := guard.InspectSecret([]byte("<rss>benign</rss>"), nil, nil, nil); err != nil {
		t.Fatalf("benign body rejected: %v", err)
	}
}
