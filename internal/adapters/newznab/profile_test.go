package newznab

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func profileTestEvidenceKey(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return "evidence." + hex.EncodeToString(digest[:16])
}

func TestNewCommissionEvidenceKeyUsesLocalBoundedFormat(t *testing.T) {
	t.Parallel()
	first, err := NewCommissionEvidenceKey()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCommissionEvidenceKey()
	if err != nil {
		t.Fatal(err)
	}
	if !evidenceKeyPattern.MatchString(first) || !evidenceKeyPattern.MatchString(second) || first == second {
		t.Fatalf("invalid locally generated evidence identifiers")
	}
}

func profileTestEvidence() CommissionEvidence {
	return CommissionEvidence{
		Key:         profileTestEvidenceKey("caps"),
		Kind:        EvidenceKindControlledProbe,
		Target:      EvidenceTarget{Kind: EvidenceTargetCAPS},
		ObservedAt:  time.Unix(1, 0).UTC(),
		BodySHA256:  strings.Repeat("a", 64),
		SafeOutcome: EvidenceOutcomeSemanticallyVerified,
	}
}

func profileTestVerifiedEvidence(target EvidenceTarget) CommissionEvidence {
	return CommissionEvidence{
		Key:         profileTestEvidenceKey("verified:" + string(target.Kind) + ":" + target.RecipeKey),
		Kind:        EvidenceKindControlledProbe,
		Target:      target,
		ObservedAt:  time.Unix(1, 0).UTC(),
		BodySHA256:  strings.Repeat("c", 64),
		SafeOutcome: EvidenceOutcomeSemanticallyVerified,
	}
}

func profileTestCAPS() CAPSRecord {
	return CAPSRecord{
		BodySHA256:    strings.Repeat("b", 64),
		ObservedAt:    time.Unix(1, 0).UTC(),
		DefaultLimit:  50,
		MaximumLimit:  100,
		ServerTitle:   "Test Newznab",
		ServerVersion: "1.0",
		Categories: []Category{
			{ID: 2000, Name: "Movies", Children: []Category{{ID: 2040, Name: "Movies HD"}}},
			{ID: 5000, Name: "TV", Children: []Category{{ID: 5040, Name: "TV HD"}}},
		},
		ModeObservations: []CAPSModeObservation{
			{
				Mode:                     SearchModeMovie,
				ElementState:             CAPSElementPresentAvailable,
				AdvertisedParameterNames: []string{"Q", "imdbid", "future", "Q"},
				RecognizedParameters:     []WireParameter{WireQuery, WireIMDbID},
			},
		},
	}
}

func profileTestMovieRecipe() SearchRecipe {
	return SearchRecipe{
		Key:           "movie.imdb",
		Scope:         SearchScopeMovie,
		Mode:          SearchModeMovie,
		Query:         QuerySpec{Style: QueryTitle},
		Bindings:      []WireBinding{{Parameter: WireIMDbID, Source: BindingIMDbNumeric}},
		Extended:      true,
		Categories:    CategorySpec{Set: CategorySelectedMovie},
		VerifiedLimit: 100,
		Role:          RecipePrimary,
		Priority:      0,
		Verification: RecipeVerification{
			State:        VerificationVerified,
			EvidenceKeys: []string{"probe.success"},
		},
	}
}

func profileTestSeasonRecipe() SearchRecipe {
	return SearchRecipe{
		Key:   "season.tvdb",
		Scope: SearchScopeCompleteSeason,
		Mode:  SearchModeTV,
		Query: QuerySpec{Style: QueryTitle},
		Bindings: []WireBinding{
			{Parameter: WireTVDbID, Source: BindingTVDbNumeric},
			{Parameter: WireSeason, Source: BindingSeasonNumber},
		},
		Extended:      true,
		Categories:    CategorySpec{Set: CategorySelectedSeries},
		VerifiedLimit: 100,
		Role:          RecipePrimary,
		Priority:      0,
		Verification: RecipeVerification{
			State:        VerificationVerified,
			EvidenceKeys: []string{"probe.success"},
		},
	}
}

func profileTestProfile(recipes ...SearchRecipe) CapabilityProfile {
	profile := CapabilityProfile{
		SchemaVersion: CapabilityProfileSchemaVersion,
		CAPS:          profileTestCAPS(),
		MappingRefs:   []MappingRef{{Key: MappingNewznabSearchV1, Version: 1}},
		Evidence:      []CommissionEvidence{profileTestEvidence()},
	}
	for _, recipe := range recipes {
		appendProfileTestRecipe(&profile, recipe)
	}
	return profile
}

func appendProfileTestRecipe(profile *CapabilityProfile, recipe SearchRecipe) {
	if len(recipe.Verification.EvidenceKeys) == 1 && recipe.Verification.EvidenceKeys[0] == "probe.success" {
		key := profileTestEvidenceKey("recipe:" + recipe.Key)
		recipe.Verification.EvidenceKeys = []string{key}
		found := false
		for _, evidence := range profile.Evidence {
			if evidence.Key == key {
				found = true
				break
			}
		}
		if !found {
			profile.Evidence = append(profile.Evidence, CommissionEvidence{
				Key:                    key,
				Kind:                   EvidenceKindControlledProbe,
				Target:                 EvidenceTarget{Kind: EvidenceTargetSearchRecipe, RecipeKey: recipe.Key},
				ObservedAt:             time.Unix(1, 0).UTC(),
				BodySHA256:             strings.Repeat("d", 64),
				SafeOutcome:            EvidenceOutcomeSearchSuccess,
				RawResultCount:         1,
				PlausibleIdentityCount: 1,
			})
		}
	}
	profile.SearchRecipes = append(profile.SearchRecipes, recipe)
}

func TestCapabilityProfileAcceptsClosedMovieAndSeasonRecipes(t *testing.T) {
	t.Parallel()
	profile := profileTestProfile(profileTestMovieRecipe(), profileTestSeasonRecipe())
	if err := ValidateCapabilityProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCapabilityProfileForActivation(profile); err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCapabilityProfile(document)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.SearchRecipes) != 2 || decoded.SearchRecipes[0].Key != "movie.imdb" {
		t.Fatalf("decoded profile lost recipes: %+v", decoded.SearchRecipes)
	}
}

func TestCapabilityProfileAllowsTruthfulEmptyLegacyDraft(t *testing.T) {
	t.Parallel()
	profile := CapabilityProfile{SchemaVersion: CapabilityProfileSchemaVersion}
	if err := ValidateCapabilityProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCapabilityProfileForActivation(profile); !errors.Is(err, ErrCapabilityProfileNotActive) {
		t.Fatalf("activation error=%v", err)
	}
}

func TestSearchRecipeValidationRejectsInvalidClosedShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*SearchRecipe)
	}{
		{name: "unsafe key", mutate: func(recipe *SearchRecipe) { recipe.Key = "https://credential.invalid/?apikey=marker" }},
		{name: "unknown scope", mutate: func(recipe *SearchRecipe) { recipe.Scope = "series" }},
		{name: "tv mode for movie", mutate: func(recipe *SearchRecipe) { recipe.Mode = SearchModeTV }},
		{name: "season query for movie", mutate: func(recipe *SearchRecipe) { recipe.Query.Style = QueryTitleSeasonToken }},
		{name: "unknown query", mutate: func(recipe *SearchRecipe) { recipe.Query.Style = "custom" }},
		{name: "query binding", mutate: func(recipe *SearchRecipe) {
			recipe.Bindings = []WireBinding{{Parameter: WireQuery, Source: BindingIMDbNumeric}}
		}},
		{name: "extended binding", mutate: func(recipe *SearchRecipe) {
			recipe.Bindings = []WireBinding{{Parameter: WireExtended, Source: BindingIMDbNumeric}}
		}},
		{name: "episode binding", mutate: func(recipe *SearchRecipe) {
			recipe.Bindings = []WireBinding{{Parameter: WireEpisode, Source: BindingSeasonNumber}}
		}},
		{name: "mismatched binding", mutate: func(recipe *SearchRecipe) {
			recipe.Bindings = []WireBinding{{Parameter: WireIMDbID, Source: BindingTMDbNumeric}}
		}},
		{name: "duplicate parameter", mutate: func(recipe *SearchRecipe) {
			recipe.Bindings = []WireBinding{{Parameter: WireIMDbID, Source: BindingIMDbNumeric}, {Parameter: WireIMDbID, Source: BindingIMDbNumeric}}
		}},
		{name: "duplicate source", mutate: func(recipe *SearchRecipe) {
			recipe.Bindings = []WireBinding{{Parameter: WireIMDbID, Source: BindingIMDbNumeric}, {Parameter: WireTMDbID, Source: BindingIMDbNumeric}}
		}},
		{name: "zero limit", mutate: func(recipe *SearchRecipe) { recipe.VerifiedLimit = 0 }},
		{name: "hard limit exceeded", mutate: func(recipe *SearchRecipe) { recipe.VerifiedLimit = MaxVerifiedRequestLimit + 1 }},
		{name: "caps limit exceeded", mutate: func(recipe *SearchRecipe) { recipe.VerifiedLimit = 100 }},
		{name: "movie uses series categories", mutate: func(recipe *SearchRecipe) { recipe.Categories.Set = CategorySelectedSeries }},
		{name: "selected categories contain fixed ids", mutate: func(recipe *SearchRecipe) { recipe.Categories.FixedIDs = []int{2000} }},
		{name: "primary has fallback triggers", mutate: func(recipe *SearchRecipe) { recipe.FallbackOn = []FallbackTrigger{FallbackZeroRawResults} }},
		{name: "fallback has no trigger", mutate: func(recipe *SearchRecipe) { recipe.Role = RecipeFallback }},
		{name: "unknown verification", mutate: func(recipe *SearchRecipe) { recipe.Verification.State = "maybe" }},
		{name: "verified without evidence", mutate: func(recipe *SearchRecipe) { recipe.Verification.EvidenceKeys = nil }},
		{name: "dangling evidence", mutate: func(recipe *SearchRecipe) { recipe.Verification.EvidenceKeys = []string{"missing"} }},
		{name: "duplicate evidence reference", mutate: func(recipe *SearchRecipe) {
			recipe.Verification.EvidenceKeys = []string{"probe.success", "probe.success"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := profileTestProfile(profileTestMovieRecipe())
			if test.name == "caps limit exceeded" {
				profile.CAPS.MaximumLimit = 50
			}
			test.mutate(&profile.SearchRecipes[0])
			if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("validation error=%v", err)
			}
		})
	}
}

func TestSearchRecipeQueryNoneAndCompleteSeasonRules(t *testing.T) {
	t.Parallel()
	movie := profileTestMovieRecipe()
	movie.Query.Style = QueryNone
	if err := ValidateCapabilityProfile(profileTestProfile(movie)); err != nil {
		t.Fatalf("identity-only movie recipe: %v", err)
	}
	movie.Bindings = []WireBinding{{Parameter: WireYear, Source: BindingYearNumber}}
	if err := ValidateCapabilityProfile(profileTestProfile(movie)); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("query-none without identity error=%v", err)
	}

	season := profileTestSeasonRecipe()
	season.Bindings = []WireBinding{{Parameter: WireTVDbID, Source: BindingTVDbNumeric}}
	if err := ValidateCapabilityProfile(profileTestProfile(season)); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("season without season conveyance error=%v", err)
	}
	season.Query.Style = QueryTitleSeasonToken
	if err := ValidateCapabilityProfile(profileTestProfile(season)); err != nil {
		t.Fatalf("season token should convey season: %v", err)
	}
	season.Query.Style = QueryNone
	season.Bindings = append(season.Bindings, WireBinding{Parameter: WireSeason, Source: BindingSeasonNumber})
	if err := ValidateCapabilityProfile(profileTestProfile(season)); err != nil {
		t.Fatalf("identity-only season recipe: %v", err)
	}
}

func TestCapabilityProfileRecipeRoleBoundsAndPriority(t *testing.T) {
	t.Parallel()
	profile := profileTestProfile()
	for index := 0; index < MaxPrimaryRecipesPerScope; index++ {
		recipe := profileTestMovieRecipe()
		recipe.Key = "movie.primary." + string(rune('a'+index))
		recipe.Priority = uint8(index)
		appendProfileTestRecipe(&profile, recipe)
	}
	for index := 0; index < MaxFallbackRecipesPerScope; index++ {
		recipe := profileTestMovieRecipe()
		recipe.Key = "movie.fallback." + string(rune('a'+index))
		recipe.Role = RecipeFallback
		recipe.Priority = uint8(index)
		recipe.FallbackOn = []FallbackTrigger{FallbackZeroRawResults}
		appendProfileTestRecipe(&profile, recipe)
	}
	if err := ValidateCapabilityProfile(profile); err != nil {
		t.Fatal(err)
	}
	extra := profileTestMovieRecipe()
	extra.Key, extra.Priority = "movie.primary.extra", 10
	appendProfileTestRecipe(&profile, extra)
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("primary bound error=%v", err)
	}

	profile = profileTestProfile(profileTestMovieRecipe())
	for index := 0; index <= MaxFallbackRecipesPerScope; index++ {
		recipe := profileTestMovieRecipe()
		recipe.Key = "movie.fallback." + string(rune('a'+index))
		recipe.Role = RecipeFallback
		recipe.Priority = uint8(index)
		recipe.FallbackOn = []FallbackTrigger{FallbackZeroRawResults}
		appendProfileTestRecipe(&profile, recipe)
	}
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("fallback bound error=%v", err)
	}

	profile = profileTestProfile(profileTestMovieRecipe())
	duplicatePriority := profileTestMovieRecipe()
	duplicatePriority.Key = "movie.second"
	appendProfileTestRecipe(&profile, duplicatePriority)
	if err := ValidateCapabilityProfile(profile); err != nil {
		t.Fatalf("equal recipe priorities should use the deterministic key tie-break: %v", err)
	}

	profile = profileTestProfile(profileTestMovieRecipe())
	duplicateKey := profileTestSeasonRecipe()
	duplicateKey.Key = profile.SearchRecipes[0].Key
	appendProfileTestRecipe(&profile, duplicateKey)
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("duplicate recipe key error=%v", err)
	}

	orphan := profileTestMovieRecipe()
	orphan.Role = RecipeFallback
	orphan.FallbackOn = []FallbackTrigger{FallbackZeroRawResults}
	if err := ValidateCapabilityProfile(profileTestProfile(orphan)); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("verified orphan fallback error=%v", err)
	}
	seasonOrphan := profileTestSeasonRecipe()
	seasonOrphan.Role = RecipeFallback
	seasonOrphan.FallbackOn = []FallbackTrigger{FallbackZeroRawResults}
	if err := ValidateCapabilityProfile(profileTestProfile(profileTestMovieRecipe(), seasonOrphan)); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("fallback used a primary from another scope: %v", err)
	}
	untestedOrphan := orphan
	untestedOrphan.Verification = RecipeVerification{State: VerificationUntested}
	if err := ValidateCapabilityProfile(profileTestProfile(untestedOrphan)); err != nil {
		t.Fatalf("untested orphan fallback should remain draft evidence: %v", err)
	}
	if err := ValidateCapabilityProfileForActivation(profileTestProfile(untestedOrphan)); !errors.Is(err, ErrCapabilityProfileNotActive) {
		t.Fatalf("untested orphan fallback activated a profile: %v", err)
	}
}

func TestCapabilityProfileCategoryAndCAPSEvidenceValidation(t *testing.T) {
	t.Parallel()
	fixed := profileTestMovieRecipe()
	fixed.Categories = CategorySpec{Set: CategoryVerifiedFixed, FixedIDs: []int{2000, 2040}}
	if err := ValidateCapabilityProfile(profileTestProfile(fixed)); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*CapabilityProfile)
	}{
		{name: "fixed category absent from caps", mutate: func(profile *CapabilityProfile) { profile.SearchRecipes[0].Categories.FixedIDs = []int{9999} }},
		{name: "fixed categories unsorted", mutate: func(profile *CapabilityProfile) { profile.SearchRecipes[0].Categories.FixedIDs = []int{2040, 2000} }},
		{name: "duplicate caps id", mutate: func(profile *CapabilityProfile) {
			profile.CAPS.Categories = append(profile.CAPS.Categories, Category{ID: 2000, Name: "Duplicate"})
		}},
		{name: "absent caps element with params", mutate: func(profile *CapabilityProfile) { profile.CAPS.ModeObservations[0].ElementState = CAPSElementAbsent }},
		{name: "recognized caps param not advertised", mutate: func(profile *CapabilityProfile) {
			profile.CAPS.ModeObservations[0].RecognizedParameters = []WireParameter{WireSeason}
		}},
		{name: "unknown caps state", mutate: func(profile *CapabilityProfile) { profile.CAPS.ModeObservations[0].ElementState = "sometimes" }},
		{name: "repeated available caps element", mutate: func(profile *CapabilityProfile) { profile.CAPS.ModeObservations[0].Repeated = true }},
		{name: "credential-bearing server title", mutate: func(profile *CapabilityProfile) {
			profile.CAPS.ServerTitle = "https://provider.invalid/?apikey=marker-secret"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := profileTestProfile(fixed)
			test.mutate(&profile)
			if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("validation error=%v", err)
			}
		})
	}
}

func TestCapabilityProfileRejectsStructurallyUnsafePersistedText(t *testing.T) {
	t.Parallel()
	unsafeValues := []struct {
		name  string
		value string
	}{
		{name: "plain URL", value: "https://provider.invalid/path"},
		{name: "one-letter URI scheme", value: "x://provider.invalid/path"},
		{name: "long URI scheme", value: strings.Repeat("a", 40) + "://provider.invalid/path"},
		{name: "opaque URI", value: "urn:provider:item"},
		{name: "arbitrary opaque URI", value: "custom:payload"},
		{name: "embedded arbitrary opaque URI", value: "provider says custom:payload"},
		{name: "percent encoded URL", value: "https%3A%2F%2Fprovider.invalid%2Fpath"},
		{name: "recursively encoded URL", value: "https%253A%252F%252Fprovider.invalid%252Fpath"},
		{name: "percent encoded userinfo URL", value: "https%3A%2F%2Fowner%3Asynthetic%40provider.invalid%2Fpath"},
		{name: "authorization bearer header", value: "Authorization: Bearer synthetic-token"},
		{name: "proxy authorization header", value: "Proxy-Authorization: Basic c3ludGhldGlj"},
		{name: "cookie header", value: "Cookie: session=synthetic"},
		{name: "set cookie header", value: "Set-Cookie: session=synthetic"},
		{name: "encoded authorization header", value: "Authorization%3A%20Bearer%20synthetic-token"},
		{name: "basic authorization value", value: "Basic c3ludGhldGljOnZhbHVl"},
		{name: "bearer authorization value", value: "Bearer synthetic-token"},
		{name: "encoded basic authorization value", value: "Basic%20c3ludGhldGljOnZhbHVl"},
		{name: "encoded bearer authorization value", value: "Bearer%20synthetic-token"},
		{name: "encoded cookie header", value: "Cookie%3A%20session%3Dsynthetic"},
		{name: "encoded set cookie header", value: "Set-Cookie%3A%20session%3Dsynthetic"},
		{name: "encoded API key parameter", value: "apikey%3Dsynthetic-value"},
		{name: "encoded underscored API key parameter", value: "api_key%3Dsynthetic-value"},
		{name: "recursively encoded API key parameter", value: "apikey%253Dsynthetic-value"},
		{name: "encoded access token parameter", value: "access_token%3Dsynthetic-value"},
		{name: "nested access token parameter", value: "access_token%253Dsynthetic-value"},
		{name: "valueless API key query", value: "provider.invalid/path?apikey"},
		{name: "valueless access token query", value: "path?mode=search&access_token"},
		{name: "encoded valueless token query", value: "path%3Ftoken"},
		{name: "protocol-relative userinfo", value: "//owner:synthetic@provider.invalid/path"},
		{name: "CRLF injection", value: "safe\r\nAuthorization: Bearer synthetic-token"},
		{name: "encoded CRLF injection", value: "safe%0D%0AAuthorization%3A%20Bearer%20synthetic-token"},
		{name: "unicode control", value: "safe\u0085unsafe"},
		{name: "invalid UTF-8", value: string([]byte{0xff})},
		{name: "percent decoded invalid UTF-8", value: "provider%C3%28title"},
		{name: "decode limit exceeded", value: "https%2525253A%2525252F%2525252Fprovider.invalid"},
		{name: "dangerous payload with incomplete trailing escape", value: "https%3A%2F%2Fprovider.invalid%"},
	}
	for _, test := range unsafeValues {
		t.Run(test.name, func(t *testing.T) {
			if validProfileText(test.value, maxProfileTextBytes) {
				t.Fatalf("unsafe text was accepted: %q", test.value)
			}
		})
	}

	for _, value := range []string{
		"Test Newznab 2.1",
		"Movies: HD / UHD (Dansk)",
		"Anime: 4K & TV: HD",
		"TV > Foreign | Nordic & European",
		"Controlled probe: valid XML; one plausible result.",
		"Movies 100%",
		"100% coverage",
		"50% complete",
		"Provider %ZZ archive",
		"Movies%20at%20100%25",
		"C++ / Film: børn, unge & familie — 4K (dansk)",
	} {
		if !validProfileText(value, maxProfileTextBytes) {
			t.Fatalf("safe ordinary text was rejected: %q", value)
		}
	}
}

func TestDecodeCapabilityProfileRejectsRawInvalidUTF8BeforeJSONDecoding(t *testing.T) {
	t.Parallel()
	const marker = "raw-invalid-utf8-marker"
	tests := []struct {
		name        string
		invalid     []byte
		setProvider func(*CapabilityProfile)
	}{
		{
			name:    "invalid continuation byte",
			invalid: []byte{0x80},
			setProvider: func(profile *CapabilityProfile) {
				profile.Evidence[0].SafeSummary = marker
			},
		},
		{
			name:    "truncated multibyte sequence",
			invalid: []byte{0xe2, 0x82},
			setProvider: func(profile *CapabilityProfile) {
				profile.CAPS.ServerVersion = marker
			},
		},
		{
			name:    "invalid provider title",
			invalid: []byte{0xff},
			setProvider: func(profile *CapabilityProfile) {
				profile.CAPS.ServerTitle = marker
			},
		},
		{
			name:    "invalid category name",
			invalid: []byte{0xc3, 0x28},
			setProvider: func(profile *CapabilityProfile) {
				profile.CAPS.Categories[0].Name = marker
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := profileTestProfile()
			test.setProvider(&profile)
			document, err := json.Marshal(profile)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(document, []byte(marker)) != 1 {
				t.Fatalf("expected exactly one raw marker in profile document")
			}
			document = bytes.Replace(document, []byte(marker), test.invalid, 1)
			if _, err := DecodeCapabilityProfile(document); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("decode error=%v", err)
			}
		})
	}
}

func TestDecodeCapabilityProfileAcceptsValidUnicodeAndLiteralPercent(t *testing.T) {
	t.Parallel()
	profile := profileTestProfile()
	profile.CAPS.ServerTitle = "Nordisk – 日本語 100%"
	profile.CAPS.Categories[0].Name = "Film: børn, unge & familie — 50% complete"
	document, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCapabilityProfile(document)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.CAPS.ServerTitle != profile.CAPS.ServerTitle || decoded.CAPS.Categories[0].Name != profile.CAPS.Categories[0].Name {
		t.Fatalf("safe provider text changed during decoding")
	}
}

func TestDecodeCapabilityProfileRejectsPercentDecodedInvalidUTF8(t *testing.T) {
	t.Parallel()
	profile := profileTestProfile()
	profile.CAPS.ServerTitle = "provider%C3%28title"
	document, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCapabilityProfile(document); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("decode error=%v", err)
	}
}

func TestDecodeProfilePercentTripletsPreservesPlusAndLiteralPercent(t *testing.T) {
	t.Parallel()
	decoded, changed := decodeProfilePercentTriplets("C++ 100% coverage%20archive%")
	if !changed || decoded != "C++ 100% coverage archive%" {
		t.Fatalf("decoded=%q changed=%v", decoded, changed)
	}
	unchanged, changed := decodeProfilePercentTriplets("C++ 100% coverage %ZZ")
	if changed || unchanged != "C++ 100% coverage %ZZ" {
		t.Fatalf("decoded=%q changed=%v", unchanged, changed)
	}
}

func TestCapabilityProfileAppliesTextSafetyToEveryProviderTextField(t *testing.T) {
	t.Parallel()
	mutations := []struct {
		name   string
		mutate func(*CapabilityProfile)
	}{
		{name: "server title", mutate: func(profile *CapabilityProfile) {
			profile.CAPS.ServerTitle = "https%253A%252F%252Fprovider.invalid"
		}},
		{name: "server version", mutate: func(profile *CapabilityProfile) {
			profile.CAPS.ServerVersion = "Authorization%3A%20Bearer%20synthetic"
		}},
		{name: "category name", mutate: func(profile *CapabilityProfile) {
			profile.CAPS.Categories[0].Name = "apikey%3Dsynthetic"
		}},
		{name: "evidence summary", mutate: func(profile *CapabilityProfile) {
			profile.Evidence[0].SafeSummary = "Set-Cookie: session=synthetic"
		}},
		{name: "server title length", mutate: func(profile *CapabilityProfile) {
			profile.CAPS.ServerTitle = strings.Repeat("a", maxProfileTextBytes+1)
		}},
		{name: "server version length", mutate: func(profile *CapabilityProfile) {
			profile.CAPS.ServerVersion = strings.Repeat("a", maxServerVersionBytes+1)
		}},
		{name: "category name length", mutate: func(profile *CapabilityProfile) {
			profile.CAPS.Categories[0].Name = strings.Repeat("a", maxProfileTextBytes+1)
		}},
		{name: "evidence summary length", mutate: func(profile *CapabilityProfile) {
			profile.Evidence[0].SafeSummary = strings.Repeat("a", maxEvidenceSummaryBytes+1)
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			profile := profileTestProfile()
			test.mutate(&profile)
			if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("validation error=%v", err)
			}
		})
	}
}

func TestCapabilityProfileUsesClosedMappingAndContractRegistries(t *testing.T) {
	t.Parallel()
	if !ValidProviderContractKey(ProviderContractNZBFinderV1) || !ValidProviderContractKey(ProviderContractPath32GetNZBV1) || !ValidProviderContractKey(ProviderContractDrunkenSlugV1) || ValidProviderContractKey("unknown.contract") {
		t.Fatal("provider-contract key registry is not closed over the reviewed v1 contracts")
	}
	profile := profileTestProfile(profileTestMovieRecipe())
	profile.MappingRefs = append(profile.MappingRefs, MappingRef{Key: "unknown.mapping", Version: 1})
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("unknown mapping error=%v", err)
	}
	profile = profileTestProfile(profileTestMovieRecipe())
	profile.MappingRefs = append(profile.MappingRefs, profile.MappingRefs[0])
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("duplicate mapping error=%v", err)
	}
	profile = profileTestProfile(profileTestMovieRecipe())
	profile.ProviderContract = &ProviderContractRef{Key: "unregistered.contract", Version: 1}
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("unknown contract error=%v", err)
	}

	registered := ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}
	profile = profileTestProfile(profileTestMovieRecipe())
	detailsEvidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetDetails})
	profile.Evidence = append(profile.Evidence, detailsEvidence)
	profile.ProviderContract = &registered
	profile.Operations = []OperationCapability{{
		Operation: OperationDetails,
		Assessment: CapabilityAssessment{
			ProviderClaim: ClaimAdvertised,
			Verification:  VerificationVerified,
			EvidenceKeys:  []string{detailsEvidence.Key},
		},
		Contract: &registered,
	}}
	if err := ValidateCapabilityProfile(profile); err != nil {
		t.Fatalf("registered aligned contract: %v", err)
	}
	mismatch := ProviderContractRef{Key: ProviderContractDrunkenSlugV1, Version: 1}
	profile.Operations[0].Contract = &mismatch
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("misaligned nested contract error=%v", err)
	}

	profile = profileTestProfile(profileTestMovieRecipe())
	profile.Operations = []OperationCapability{{Operation: OperationNFO, Assessment: CapabilityAssessment{Verification: VerificationUntested}}}
	if err := ValidateCapabilityProfile(profile); err != nil {
		t.Fatalf("operation with non-applicable omitted claims: %v", err)
	}
	profile.Operations[0].Assessment.CAPSClaim = ClaimOmitted
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("non-applicable CAPS claim error=%v", err)
	}
	profile.Operations[0].Assessment.CAPSClaim = ""
	profile.Operations[0].Assessment = CapabilityAssessment{Verification: VerificationVerified, EvidenceKeys: []string{"probe.success", "probe.success"}}
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("duplicate assessment evidence error=%v", err)
	}
}

func TestProviderContractRegistryRequiresExactCanonicalKeys(t *testing.T) {
	t.Parallel()
	canonicalKeys := []string{
		ProviderContractNZBFinderV1,
		ProviderContractPath32GetNZBV1,
		ProviderContractDrunkenSlugV1,
	}
	for _, key := range canonicalKeys {
		if !ValidProviderContractKey(key) {
			t.Errorf("canonical provider-contract key %q was rejected", key)
		}
	}

	invalidKeys := []struct {
		name string
		key  string
	}{
		{name: "empty", key: ""},
		{name: "leading ASCII space", key: " " + ProviderContractNZBFinderV1},
		{name: "trailing ASCII space", key: ProviderContractNZBFinderV1 + " "},
		{name: "leading tab", key: "\t" + ProviderContractNZBFinderV1},
		{name: "trailing tab", key: ProviderContractNZBFinderV1 + "\t"},
		{name: "leading newline", key: "\n" + ProviderContractNZBFinderV1},
		{name: "trailing newline", key: ProviderContractNZBFinderV1 + "\n"},
		{name: "carriage return", key: ProviderContractNZBFinderV1 + "\r"},
		{name: "CRLF", key: ProviderContractNZBFinderV1 + "\r\n"},
		{name: "non-breaking space", key: "\u00a0" + ProviderContractNZBFinderV1},
		{name: "Unicode em space", key: ProviderContractNZBFinderV1 + "\u2003"},
		{name: "excessive leading padding", key: strings.Repeat(" ", 4096) + ProviderContractNZBFinderV1},
		{name: "excessive trailing padding", key: ProviderContractNZBFinderV1 + strings.Repeat(" ", 4096)},
		{name: "whitespace only", key: " \t\r\n"},
		{name: "embedded newline", key: "nzb\nfinder"},
		{name: "embedded NUL", key: "nzb\x00finder"},
		{name: "invalid UTF-8", key: string([]byte("nzbfinder\xff"))},
		{name: "wrong case", key: "NZBFINDER"},
		{name: "unknown", key: "unknown.contract"},
	}
	for _, test := range invalidKeys {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if ValidProviderContractKey(test.key) {
				t.Fatal("noncanonical provider-contract key was accepted")
			}
			profile := profileTestProfile(profileTestMovieRecipe())
			profile.ProviderContract = &ProviderContractRef{Key: test.key, Version: 1}
			if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("noncanonical profile contract error=%v", err)
			}
			document, err := json.Marshal(profile)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeCapabilityProfile(document); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("decoded noncanonical profile contract error=%v", err)
			}
		})
	}

	canonical := ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}
	profile := profileTestProfile(profileTestMovieRecipe())
	profile.ProviderContract = &canonical
	if err := ValidateCapabilityProfile(profile); err != nil {
		t.Fatalf("canonical profile contract: %v", err)
	}
	document, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCapabilityProfile(document)
	if err != nil {
		t.Fatalf("decode canonical profile contract: %v", err)
	}
	if decoded.ProviderContract == nil || *decoded.ProviderContract != canonical {
		t.Fatalf("canonical contract changed during decode: %#v", decoded.ProviderContract)
	}

	for _, version := range []uint16{0, 2} {
		profile.ProviderContract = &ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: version}
		if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
			t.Fatalf("contract version %d error=%v", version, err)
		}
	}
}

func TestCapabilityProfileKeepsRetrievalResponsibilitiesSeparate(t *testing.T) {
	t.Parallel()
	contract := ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}
	untested := CapabilityAssessment{ProviderClaim: ClaimUnknown, Verification: VerificationUntested}
	draft := profileTestProfile(profileTestMovieRecipe())
	draft.Retrieval = RetrievalProfile{
		NativeIdentity: NativeIdentityCapability{Assessment: untested, Method: NativeIdentityGUID},
		StandardGet:    untested,
	}
	if err := ValidateCapabilityProfile(draft); err != nil {
		t.Fatalf("unselected untested retrieval declaration: %v", err)
	}

	profile := profileTestProfile(profileTestMovieRecipe())
	nativeEvidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetNativeIdentity})
	getEvidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetStandardGet})
	profile.Evidence = append(profile.Evidence, nativeEvidence, getEvidence)
	profile.ProviderContract = &contract
	profile.Retrieval = RetrievalProfile{
		NativeIdentity: NativeIdentityCapability{
			Assessment: CapabilityAssessment{ProviderClaim: ClaimAdvertised, Verification: VerificationVerified, EvidenceKeys: []string{nativeEvidence.Key}},
			Method:     NativeIdentityDetailsContract,
			Contract:   &contract,
		},
		StandardGet:     CapabilityAssessment{ProviderClaim: ClaimAdvertised, Verification: VerificationVerified, EvidenceKeys: []string{getEvidence.Key}},
		PreferredMethod: RetrievalStandardGet,
	}
	if err := ValidateCapabilityProfile(profile); err != nil {
		t.Fatalf("separate identity extraction and standard retrieval: %v", err)
	}

	profile.Retrieval.StandardGet.CAPSClaim = ClaimAdvertised
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("retrieval accepted a non-applicable CAPS claim: %v", err)
	}
	profile.Retrieval.StandardGet.CAPSClaim = ""
	profile.Retrieval.PreferredMethod = RetrievalAdvertisedLocator
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("profile preferred an undeclared locator: %v", err)
	}

	locatorEvidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetAdvertisedLocator})
	profile.Evidence = append(profile.Evidence, locatorEvidence)
	profile.Retrieval.AdvertisedLocator = &AdvertisedLocatorCapability{
		Assessment: CapabilityAssessment{ProviderClaim: ClaimAdvertised, Verification: VerificationVerified, EvidenceKeys: []string{locatorEvidence.Key}},
		Validator:  contract,
	}
	if err := ValidateCapabilityProfile(profile); err != nil {
		t.Fatalf("verified advertised locator declaration: %v", err)
	}
	profile.Retrieval.AdvertisedLocator.Assessment.Verification = VerificationUntested
	profile.Retrieval.AdvertisedLocator.Assessment.EvidenceKeys = nil
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("profile preferred an unverified locator: %v", err)
	}
}

func TestCapabilityVerificationRejectsTemporaryOrNoResultEvidence(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"no_results", "timeout", "rate_limited", "auth_failed", "server_failure"} {
		outcome := outcome
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			profile := profileTestProfile(profileTestMovieRecipe())
			profile.Evidence[1].SafeOutcome = EvidenceOutcome(outcome)
			profile.Evidence[1].RawResultCount = 0
			profile.Evidence[1].PlausibleIdentityCount = 0
			if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("search verification accepted %q evidence: %v", outcome, err)
			}

			profile = profileTestProfile()
			detailsEvidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetDetails})
			detailsEvidence.SafeOutcome = EvidenceOutcome(outcome)
			profile.Evidence = append(profile.Evidence, detailsEvidence)
			profile.Operations = []OperationCapability{{
				Operation: OperationDetails,
				Assessment: CapabilityAssessment{
					Verification: VerificationVerified,
					EvidenceKeys: []string{detailsEvidence.Key},
				},
			}}
			if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("operation verification accepted %q evidence: %v", outcome, err)
			}
		})
	}
}

func TestCapabilityVerificationRequiresStateCompatibleRecipeEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		state   VerificationState
		outcome EvidenceOutcome
	}{
		{state: VerificationVerified, outcome: EvidenceOutcomeSearchSuccess},
		{state: VerificationUnsupported, outcome: EvidenceOutcomeUnsupported},
		{state: VerificationPermissionRestricted, outcome: EvidenceOutcomePermissionRestricted},
		{state: VerificationContradicted, outcome: EvidenceOutcomeContradicted},
	} {
		test := test
		t.Run(string(test.state), func(t *testing.T) {
			t.Parallel()
			profile := profileTestProfile(profileTestMovieRecipe())
			profile.SearchRecipes[0].Verification.State = test.state
			profile.Evidence[1].SafeOutcome = test.outcome
			if err := ValidateCapabilityProfile(profile); err != nil {
				t.Fatalf("matching %q evidence was rejected: %v", test.outcome, err)
			}
			profile.Evidence[1].SafeOutcome = EvidenceOutcomePermissionRestricted
			if test.state == VerificationPermissionRestricted {
				profile.Evidence[1].SafeOutcome = EvidenceOutcomeContradicted
			}
			if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("state %q accepted incompatible evidence: %v", test.state, err)
			}
		})
	}
}

func TestCapabilityVerificationEvidenceIsBoundToItsExactRecipe(t *testing.T) {
	t.Parallel()
	first := profileTestMovieRecipe()
	second := profileTestMovieRecipe()
	second.Key = "movie.tmdb"
	second.Priority = 1
	second.Bindings = []WireBinding{{Parameter: WireTMDbID, Source: BindingTMDbNumeric}}
	profile := profileTestProfile(first, second)
	profile.SearchRecipes[1].Verification.EvidenceKeys = append([]string(nil), profile.SearchRecipes[0].Verification.EvidenceKeys...)
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("one recipe reused another recipe's evidence: %v", err)
	}
	profile = profileTestProfile(first, second)
	profile.SearchRecipes[0].Verification.EvidenceKeys = append(profile.SearchRecipes[0].Verification.EvidenceKeys, profile.SearchRecipes[1].Verification.EvidenceKeys[0])
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("recipe accepted a mixed exact/cross-target evidence list: %v", err)
	}

	profile = profileTestProfile(profileTestMovieRecipe())
	profile.Operations = []OperationCapability{{
		Operation: OperationDetails,
		Assessment: CapabilityAssessment{
			Verification: VerificationVerified,
			EvidenceKeys: append([]string(nil), profile.SearchRecipes[0].Verification.EvidenceKeys...),
		},
	}}
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("operation reused recipe-bound evidence: %v", err)
	}
}

func TestCapabilityVerificationRejectsCrossOperationEvidenceReuse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		evidenceTarget EvidenceTargetKind
		operation      Operation
	}{
		{name: "details cannot verify nfo", evidenceTarget: EvidenceTargetDetails, operation: OperationNFO},
		{name: "nfo cannot verify file list", evidenceTarget: EvidenceTargetNFO, operation: OperationFileList},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := profileTestProfile()
			evidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: test.evidenceTarget})
			profile.Evidence = append(profile.Evidence, evidence)
			profile.Operations = []OperationCapability{{
				Operation: test.operation,
				Assessment: CapabilityAssessment{
					Verification: VerificationVerified,
					EvidenceKeys: []string{evidence.Key},
				},
			}}
			if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("cross-target evidence error=%v", err)
			}
		})
	}

	for _, test := range []struct {
		state   VerificationState
		outcome EvidenceOutcome
	}{
		{state: VerificationUnsupported, outcome: EvidenceOutcomeUnsupported},
		{state: VerificationPermissionRestricted, outcome: EvidenceOutcomePermissionRestricted},
		{state: VerificationContradicted, outcome: EvidenceOutcomeContradicted},
	} {
		profile := profileTestProfile()
		evidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetDetails})
		evidence.SafeOutcome = test.outcome
		profile.Evidence = append(profile.Evidence, evidence)
		profile.Operations = []OperationCapability{{
			Operation: OperationDetails,
			Assessment: CapabilityAssessment{
				Verification: test.state,
				EvidenceKeys: []string{evidence.Key},
			},
		}}
		if err := ValidateCapabilityProfile(profile); err != nil {
			t.Fatalf("state %q rejected its exact target evidence: %v", test.state, err)
		}
		profile.Operations[0].Operation = OperationNFO
		if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
			t.Fatalf("state %q reused another operation's evidence: %v", test.state, err)
		}
	}

	profile := profileTestProfile()
	detailsEvidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetDetails})
	nfoEvidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetNFO})
	profile.Evidence = append(profile.Evidence, detailsEvidence, nfoEvidence)
	profile.Operations = []OperationCapability{{
		Operation: OperationDetails,
		Assessment: CapabilityAssessment{
			Verification: VerificationVerified,
			EvidenceKeys: []string{detailsEvidence.Key, nfoEvidence.Key},
		},
	}}
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("operation accepted a mixed exact/cross-target evidence list: %v", err)
	}
}

func TestCapabilityVerificationRejectsCrossRetrievalEvidenceReuse(t *testing.T) {
	t.Parallel()
	contract := ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}
	nativeEvidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetNativeIdentity})
	getEvidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetStandardGet})
	locatorEvidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetAdvertisedLocator})
	directEvidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetProviderDirect})
	valid := profileTestProfile()
	valid.ProviderContract = &contract
	valid.Evidence = append(valid.Evidence, nativeEvidence, getEvidence, locatorEvidence, directEvidence)
	valid.Retrieval = RetrievalProfile{
		NativeIdentity: NativeIdentityCapability{
			Assessment: CapabilityAssessment{Verification: VerificationVerified, EvidenceKeys: []string{nativeEvidence.Key}},
			Method:     NativeIdentityDetailsContract,
			Contract:   &contract,
		},
		StandardGet: CapabilityAssessment{Verification: VerificationVerified, EvidenceKeys: []string{getEvidence.Key}},
		AdvertisedLocator: &AdvertisedLocatorCapability{
			Assessment: CapabilityAssessment{Verification: VerificationVerified, EvidenceKeys: []string{locatorEvidence.Key}},
			Validator:  contract,
		},
		DirectDownload: &DirectDownloadCapability{
			Assessment: CapabilityAssessment{Verification: VerificationVerified, EvidenceKeys: []string{directEvidence.Key}},
			Contract:   contract,
		},
		PreferredMethod: RetrievalProviderDirect,
	}
	if err := ValidateCapabilityProfile(valid); err != nil {
		t.Fatalf("distinct target records from one physical probe should validate: %v", err)
	}
	mixed := valid
	mixed.Retrieval.StandardGet.EvidenceKeys = append(mixed.Retrieval.StandardGet.EvidenceKeys, nativeEvidence.Key)
	if err := ValidateCapabilityProfile(mixed); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("retrieval declaration accepted a mixed exact/cross-target evidence list: %v", err)
	}

	tests := []struct {
		name           string
		evidenceTarget EvidenceTargetKind
		declaration    EvidenceTargetKind
	}{
		{name: "operation cannot verify retrieval", evidenceTarget: EvidenceTargetDetails, declaration: EvidenceTargetStandardGet},
		{name: "native identity cannot verify standard get", evidenceTarget: EvidenceTargetNativeIdentity, declaration: EvidenceTargetStandardGet},
		{name: "standard get cannot verify advertised locator", evidenceTarget: EvidenceTargetStandardGet, declaration: EvidenceTargetAdvertisedLocator},
		{name: "advertised locator cannot verify provider direct", evidenceTarget: EvidenceTargetAdvertisedLocator, declaration: EvidenceTargetProviderDirect},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := profileTestProfile()
			profile.ProviderContract = &contract
			evidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: test.evidenceTarget})
			profile.Evidence = append(profile.Evidence, evidence)
			verified := CapabilityAssessment{Verification: VerificationVerified, EvidenceKeys: []string{evidence.Key}}
			untested := CapabilityAssessment{Verification: VerificationUntested}
			profile.Retrieval = RetrievalProfile{
				NativeIdentity: NativeIdentityCapability{Assessment: untested, Method: NativeIdentityGUID},
				StandardGet:    untested,
			}
			switch test.declaration {
			case EvidenceTargetStandardGet:
				profile.Retrieval.StandardGet = verified
			case EvidenceTargetAdvertisedLocator:
				profile.Retrieval.AdvertisedLocator = &AdvertisedLocatorCapability{Assessment: verified, Validator: contract}
			case EvidenceTargetProviderDirect:
				profile.Retrieval.DirectDownload = &DirectDownloadCapability{Assessment: verified, Contract: contract}
			}
			if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("cross-target evidence error=%v", err)
			}
		})
	}
}

func TestCapabilityEvidenceUsesClosedTargetsKindsOutcomesAndLocalKeys(t *testing.T) {
	t.Parallel()
	valid := profileTestProfile()
	valid.Evidence[0].SafeSummary = "Controlled CAPS probe returned a valid XML envelope."
	if err := ValidateCapabilityProfile(valid); err != nil {
		t.Fatalf("safe evidence summary: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*CommissionEvidence)
	}{
		{name: "provider supplied key", mutate: func(evidence *CommissionEvidence) { evidence.Key = "provider.supplied" }},
		{name: "unknown kind", mutate: func(evidence *CommissionEvidence) { evidence.Kind = "provider_probe" }},
		{name: "unknown outcome", mutate: func(evidence *CommissionEvidence) { evidence.SafeOutcome = "temporary_success" }},
		{name: "search outcome for operation target", mutate: func(evidence *CommissionEvidence) { evidence.SafeOutcome = EvidenceOutcomeSearchSuccess }},
		{name: "unknown target", mutate: func(evidence *CommissionEvidence) { evidence.Target.Kind = "custom_target" }},
		{name: "search target without recipe", mutate: func(evidence *CommissionEvidence) { evidence.Target = EvidenceTarget{Kind: EvidenceTargetSearchRecipe} }},
		{name: "operation target with recipe", mutate: func(evidence *CommissionEvidence) { evidence.Target.RecipeKey = "movie.imdb" }},
		{name: "non-search result counts", mutate: func(evidence *CommissionEvidence) { evidence.RawResultCount = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := profileTestProfile()
			test.mutate(&profile.Evidence[0])
			if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("validation error=%v", err)
			}
		})
	}

	for _, test := range []struct {
		name      string
		outcome   EvidenceOutcome
		rawCount  int
		plausible int
	}{
		{name: "no-results with raw results", outcome: EvidenceOutcomeNoResults, rawCount: 1},
		{name: "no-results with plausible results", outcome: EvidenceOutcomeNoResults, rawCount: 1, plausible: 1},
		{name: "search success without raw results", outcome: EvidenceOutcomeSearchSuccess},
	} {
		t.Run(test.name, func(t *testing.T) {
			recipe := profileTestMovieRecipe()
			recipe.Verification = RecipeVerification{State: VerificationUntested}
			profile := profileTestProfile(recipe)
			evidence := profileTestVerifiedEvidence(EvidenceTarget{Kind: EvidenceTargetSearchRecipe, RecipeKey: recipe.Key})
			evidence.SafeOutcome = test.outcome
			evidence.RawResultCount = test.rawCount
			evidence.PlausibleIdentityCount = test.plausible
			profile.Evidence = append(profile.Evidence, evidence)
			if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
				t.Fatalf("inconsistent search outcome counts were accepted: %v", err)
			}
		})
	}
}

func TestProviderContractEndpointRegistryRequiresExactReviewedOrigin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		reference ProviderContractRef
		endpoint  string
		want      bool
	}{
		{name: "NZBFinder canonical HTTPS", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}, endpoint: "https://NZBFinder.ws:443/api", want: true},
		{name: "path32 known origin", reference: ProviderContractRef{Key: ProviderContractPath32GetNZBV1, Version: 1}, endpoint: "https://api.nzb.life/api", want: true},
		{name: "DrunkenSlug canonical HTTPS", reference: ProviderContractRef{Key: ProviderContractDrunkenSlugV1, Version: 1}, endpoint: "https://drunkenslug.com/api", want: true},
		{name: "wrong origin", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}, endpoint: "https://provider.invalid/api"},
		{name: "wrong contract", reference: ProviderContractRef{Key: ProviderContractDrunkenSlugV1, Version: 1}, endpoint: "https://nzbfinder.ws/api"},
		{name: "nonstandard port", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}, endpoint: "https://nzbfinder.ws:444/api"},
		{name: "insecure scheme", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}, endpoint: "http://nzbfinder.ws/api"},
		{name: "userinfo", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 1}, endpoint: "https://owner@nzbfinder.ws/api"},
		{name: "unknown version", reference: ProviderContractRef{Key: ProviderContractNZBFinderV1, Version: 2}, endpoint: "https://nzbfinder.ws/api"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ProviderContractAcceptsEndpoint(test.reference, test.endpoint); got != test.want {
				t.Fatalf("accepted=%v, want %v", got, test.want)
			}
		})
	}
}

func TestDecodeCapabilityProfileRejectsHiddenFieldsTrailingDataAndLeaks(t *testing.T) {
	t.Parallel()
	marker := "marker-secret-value"
	documents := [][]byte{
		[]byte(`{"schema_version":1,"caps":{},"search_recipes":[],"operations":[],"retrieval":{},"mapping_refs":[],"evidence":[],"api_key":"` + marker + `"}`),
		[]byte(`{"schema_version":1,"caps":{},"search_recipes":[],"operations":[],"retrieval":{},"mapping_refs":[],"evidence":[],"base_url":"https://credential.invalid/"}`),
		[]byte(`{"schema_version":1,"caps":{},"search_recipes":[{"key":"legacy","scope":"movie","mode":"search","query":{"style":"title"},"bindings":[],"extended":false,"categories":{"set":"none"},"verified_limit":1,"role":"primary","priority":0,"verification":{"state":"untested"},"output":"json"}],"operations":[],"retrieval":{},"mapping_refs":[],"evidence":[]}`),
		[]byte(`{"schema_version":1,"caps":{},"search_recipes":[{"key":"legacy","scope":"movie","mode":"search","query":{"style":"title"},"bindings":[],"extended":false,"categories":{"set":"none"},"verified_limit":1,"role":"primary","priority":0,"verification":{"state":"untested"},"pagination":"offset"}],"operations":[],"retrieval":{},"mapping_refs":[],"evidence":[]}`),
		[]byte(`{"schema_version":1,"caps":{},"search_recipes":[],"operations":[],"retrieval":{},"mapping_refs":[],"evidence":[]} {}`),
	}
	for _, document := range documents {
		_, err := DecodeCapabilityProfile(document)
		if !errors.Is(err, ErrInvalidCapabilityProfile) {
			t.Fatalf("decode error=%v", err)
		}
		if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), "credential.invalid") {
			t.Fatalf("unsafe input leaked in error: %v", err)
		}
	}
}

func TestCapabilityProfileNonUntestedStatesRequireEvidence(t *testing.T) {
	t.Parallel()
	for _, state := range []VerificationState{VerificationVerified, VerificationUnsupported, VerificationPermissionRestricted, VerificationContradicted} {
		recipe := profileTestMovieRecipe()
		recipe.Verification.State = state
		recipe.Verification.EvidenceKeys = nil
		if err := ValidateCapabilityProfile(profileTestProfile(recipe)); !errors.Is(err, ErrInvalidCapabilityProfile) {
			t.Fatalf("state %q accepted without evidence", state)
		}
	}
	recipe := profileTestMovieRecipe()
	recipe.Verification = RecipeVerification{State: VerificationUntested}
	if err := ValidateCapabilityProfile(profileTestProfile(recipe)); err != nil {
		t.Fatalf("untested legacy recipe should be valid: %v", err)
	}

	noResult := profileTestProfile(profileTestMovieRecipe())
	noResult.Evidence[1].SafeOutcome = "no_results"
	noResult.Evidence[1].RawResultCount = 0
	noResult.Evidence[1].PlausibleIdentityCount = 0
	if err := ValidateCapabilityProfile(noResult); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("no-result probe verified a recipe: %v", err)
	}
}

func TestCapabilityProfileRequiresDurableEvidenceForPersistentClaims(t *testing.T) {
	t.Parallel()
	profile := profileTestProfile(profileTestMovieRecipe())
	profile.Evidence[1].ObservedAt = time.Time{}
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("verified recipe accepted evidence without an observation time: %v", err)
	}
	profile = profileTestProfile(profileTestMovieRecipe())
	profile.Evidence[1].BodySHA256 = ""
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("verified recipe accepted evidence without a body digest: %v", err)
	}
	profile = profileTestProfile(profileTestMovieRecipe())
	profile.CAPS.ObservedAt = time.Time{}
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("nonempty CAPS evidence accepted without an observation time: %v", err)
	}
	profile = profileTestProfile(profileTestMovieRecipe())
	profile.Evidence[1].RawResultCount = maxCommissionResultCount
	profile.Evidence[1].PlausibleIdentityCount = maxCommissionResultCount
	if err := ValidateCapabilityProfile(profile); err != nil {
		t.Fatalf("bounded evidence count was rejected: %v", err)
	}
	profile.Evidence[1].RawResultCount++
	if err := ValidateCapabilityProfile(profile); !errors.Is(err, ErrInvalidCapabilityProfile) {
		t.Fatalf("unbounded evidence count was accepted: %v", err)
	}
}
