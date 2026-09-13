package commissioningv2

import (
	"sort"
	"strings"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/releases"
)

// SearchProof is the closed, versioned semantic search proof payload
// (architecture section 20). Counts alone are insufficient; every field is a
// closed enum, a bounded count, or a requested number. It never stores a full
// result title, URL, GUID, arbitrary attribute, raw XML, or provider error
// message.
type SearchProof struct {
	SchemaVersion          uint16   `json:"schema_version"`
	Scope                  string   `json:"scope"`
	ProviderOutcome        string   `json:"provider_outcome"`
	IdentityNamespace      string   `json:"identity_namespace,omitempty"`
	IdentityMatch          string   `json:"identity_match"`
	TitleMatch             string   `json:"title_match"`
	YearMatch              string   `json:"year_match"`
	SeasonRequested        uint16   `json:"season_requested,omitempty"`
	SeasonMatch            string   `json:"season_match"`
	SeasonTokenMatch       bool     `json:"season_token_match,omitempty"`
	CompletePackBasis      string   `json:"complete_pack_basis"`
	IndividualEpisodeCount uint16   `json:"individual_episode_count"`
	MalformedItemCount     uint16   `json:"malformed_item_count"`
	ValidItemCount         uint16   `json:"valid_item_count"`
	RawResultCount         uint16   `json:"raw_result_count"`
	PlausibleIdentityCount uint16   `json:"plausible_identity_count"`
	ConflictCodes          []string `json:"conflict_codes,omitempty"`
}

const (
	searchProviderNone                 = "none"
	searchProviderUnsupported          = "unsupported"
	searchProviderPermissionRestricted = "permission_restricted"

	searchIdentityObserved    = "observed"
	searchIdentityAbsent      = "absent"
	searchIdentityConflicting = "conflicting"

	searchTitleExact = "exact"
	searchTitleNone  = "none"

	searchYearExact       = "exact"
	searchYearAbsent      = "absent"
	searchYearConflicting = "conflicting"
	searchYearNotRequired = "not_required"

	searchSeasonExact       = "exact"
	searchSeasonAbsent      = "absent"
	searchSeasonConflicting = "conflicting"
	searchSeasonNotRequired = "not_required"

	searchPackProviderField = "provider_field"
	searchPackTitleGrammar  = "title_grammar"
	searchPackNone          = "none"
)

// closedSearchConflictCodes is the exact allowed conflict-code set.
var closedSearchConflictCodes = map[string]bool{
	"identity_conflict":        true,
	"year_conflict":            true,
	"season_conflict":          true,
	"individual_episodes_only": true,
}

// ValidateSearchProof enforces the closed proof shape and bounds.
func ValidateSearchProof(proof SearchProof) error {
	if proof.SchemaVersion != DocumentSchemaVersion {
		return ErrInvalidCommissioningDocument
	}
	if proof.Scope != string(newznab.SearchScopeMovie) && proof.Scope != string(newznab.SearchScopeCompleteSeason) {
		return ErrInvalidCommissioningDocument
	}
	switch proof.ProviderOutcome {
	case searchProviderNone, searchProviderUnsupported, searchProviderPermissionRestricted:
	default:
		return ErrInvalidCommissioningDocument
	}
	switch proof.IdentityMatch {
	case searchIdentityObserved, searchIdentityAbsent, searchIdentityConflicting:
	default:
		return ErrInvalidCommissioningDocument
	}
	switch proof.TitleMatch {
	case searchTitleExact, searchTitleNone:
	default:
		return ErrInvalidCommissioningDocument
	}
	switch proof.YearMatch {
	case searchYearExact, searchYearAbsent, searchYearConflicting, searchYearNotRequired:
	default:
		return ErrInvalidCommissioningDocument
	}
	switch proof.SeasonMatch {
	case searchSeasonExact, searchSeasonAbsent, searchSeasonConflicting, searchSeasonNotRequired:
	default:
		return ErrInvalidCommissioningDocument
	}
	switch proof.CompletePackBasis {
	case searchPackProviderField, searchPackTitleGrammar, searchPackNone:
	default:
		return ErrInvalidCommissioningDocument
	}
	if proof.Scope == string(newznab.SearchScopeMovie) {
		if proof.SeasonMatch != searchSeasonNotRequired || proof.SeasonRequested != 0 || proof.SeasonTokenMatch {
			return ErrInvalidCommissioningDocument
		}
	} else if proof.SeasonRequested == 0 {
		return ErrInvalidCommissioningDocument
	}
	if proof.ProviderOutcome != searchProviderNone && proof.RawResultCount != 0 {
		return ErrInvalidCommissioningDocument
	}
	if proof.PlausibleIdentityCount > proof.RawResultCount || proof.IndividualEpisodeCount > proof.RawResultCount || proof.MalformedItemCount > proof.RawResultCount || proof.ValidItemCount > proof.RawResultCount || proof.MalformedItemCount+proof.ValidItemCount != proof.RawResultCount {
		return ErrInvalidCommissioningDocument
	}
	if proof.RawResultCount > defaultProbeLimit {
		return ErrInvalidCommissioningDocument
	}
	if len(proof.ConflictCodes) > 8 {
		return ErrInvalidCommissioningDocument
	}
	previous := ""
	for _, code := range proof.ConflictCodes {
		if !closedSearchConflictCodes[code] || code <= previous {
			return ErrInvalidCommissioningDocument
		}
		previous = code
	}
	return nil
}

type searchProofAccumulator struct {
	scope               newznab.SearchScope
	targetIdentity      string
	providerOutcome     string
	identityMatch       string
	titleMatch          string
	yearMatch           string
	seasonMatch         string
	seasonToken         bool
	packBasis           string
	raw                 uint16
	plausible           uint16
	individual          uint16
	individualPlausible uint16
	malformed           uint16
	valid               uint16
	conflictCodes       map[string]bool
}

func newSearchProofAccumulator(scope newznab.SearchScope) *searchProofAccumulator {
	accumulator := &searchProofAccumulator{
		scope:           scope,
		providerOutcome: searchProviderNone,
		identityMatch:   searchIdentityAbsent,
		titleMatch:      searchTitleNone,
		packBasis:       searchPackNone,
		conflictCodes:   map[string]bool{},
	}
	if scope == newznab.SearchScopeMovie {
		accumulator.yearMatch = searchYearAbsent
		accumulator.seasonMatch = searchSeasonNotRequired
	} else {
		accumulator.yearMatch = searchYearNotRequired
		accumulator.seasonMatch = searchSeasonAbsent
	}
	return accumulator
}

func (accumulator *searchProofAccumulator) addConflict(code string) {
	accumulator.conflictCodes[code] = true
}

// BuildSearchProof derives the closed semantic proof from the parsed,
// safety-checked search observations. A malformed individual item is counted
// and isolated: it never contributes to identity/title/year/season/pack
// aggregation and can never help a probe verify.
func BuildSearchProof(scope newznab.SearchScope, fixture searchFixture, observations []releases.Observation) (SearchProof, error) {
	accumulator := newSearchProofAccumulator(scope)
	accumulator.targetIdentity = fixture.identityNamespace
	for index := range observations {
		observation := releases.InferTitleFacts(observations[index], observations[index].ObservedAt)
		accumulator.raw++
		if observationMalformed(observation) {
			accumulator.malformed++
			continue
		}
		accumulator.valid++
		accumulator.observe(observation, fixture)
	}
	if accumulator.raw > defaultProbeLimit {
		return SearchProof{}, ErrInvalidCommissioningDocument
	}
	if scope == newznab.SearchScopeCompleteSeason && accumulator.plausible == 0 && accumulator.individualPlausible > 0 {
		accumulator.addConflict("individual_episodes_only")
	}
	proof := SearchProof{
		SchemaVersion:          DocumentSchemaVersion,
		Scope:                  string(scope),
		ProviderOutcome:        accumulator.providerOutcome,
		IdentityNamespace:      accumulator.targetIdentity,
		IdentityMatch:          accumulator.identityMatch,
		TitleMatch:             accumulator.titleMatch,
		YearMatch:              accumulator.yearMatch,
		SeasonRequested:        fixture.season,
		SeasonMatch:            accumulator.seasonMatch,
		SeasonTokenMatch:       accumulator.seasonToken,
		CompletePackBasis:      accumulator.packBasis,
		IndividualEpisodeCount: accumulator.individual,
		MalformedItemCount:     accumulator.malformed,
		ValidItemCount:         accumulator.valid,
		RawResultCount:         accumulator.raw,
		PlausibleIdentityCount: accumulator.plausible,
	}
	for code := range accumulator.conflictCodes {
		proof.ConflictCodes = append(proof.ConflictCodes, code)
	}
	sort.Strings(proof.ConflictCodes)
	if err := ValidateSearchProof(proof); err != nil {
		return SearchProof{}, err
	}
	return proof, nil
}

func observationMalformed(observation releases.Observation) bool {
	for _, problem := range observation.Problems {
		if strings.Contains(problem, "title missing") || strings.Contains(problem, "conflicting Newznab guid") {
			return true
		}
	}
	return false
}

func (accumulator *searchProofAccumulator) observe(observation releases.Observation, fixture searchFixture) {
	identityObserved, identityConflicting := observationIdentity(observation, fixture)
	if identityConflicting {
		accumulator.identityMatch = searchIdentityConflicting
		accumulator.addConflict("identity_conflict")
	} else if identityObserved && accumulator.identityMatch != searchIdentityConflicting {
		accumulator.identityMatch = searchIdentityObserved
	}
	titleExact := releases.TitleMatches(observation.Title, fixture.title)
	if titleExact {
		accumulator.titleMatch = searchTitleExact
	}
	if fixture.scope == newznab.SearchScopeMovie {
		if accumulator.observeMovie(observation, fixture, titleExact, identityObserved, identityConflicting) {
			accumulator.plausible++
		}
		return
	}
	if accumulator.observeSeason(observation, fixture, titleExact, identityObserved, identityConflicting) {
		accumulator.plausible++
	}
}

// observeMovie reports whether this single observation independently meets the
// movie verification conditions.
func (accumulator *searchProofAccumulator) observeMovie(observation releases.Observation, fixture searchFixture, titleExact, identityObserved, identityConflicting bool) bool {
	yearExact, yearPresent := false, false
	if fixture.year != nil {
		if year, present := titleYear(observation.Title); present {
			yearPresent = true
			if year == *fixture.year {
				yearExact = true
				accumulator.yearMatch = searchYearExact
			} else if identityObserved || titleExact {
				accumulator.yearMatch = searchYearConflicting
				accumulator.addConflict("year_conflict")
			}
		}
	} else {
		accumulator.yearMatch = searchYearNotRequired
	}
	if identityConflicting {
		return false
	}
	if identityObserved && (fixture.year == nil || !yearPresent || yearExact) {
		return true
	}
	if !fixture.requireIdentity && titleExact && fixture.year != nil && yearExact {
		return true
	}
	return false
}

// titleYear extracts a bounded four-digit year token from a release title. A
// missing year is absence, never a conflict.
func titleYear(title string) (uint16, bool) {
	for _, token := range strings.FieldsFunc(title, func(value rune) bool { return value < '0' || value > '9' }) {
		if len(token) != 4 {
			continue
		}
		parsed := 0
		for _, character := range token {
			parsed = parsed*10 + int(character-'0')
		}
		if parsed >= 1900 && parsed <= 2100 {
			return uint16(parsed), true
		}
	}
	return 0, false
}

// observeSeason reports whether this single observation independently meets the
// complete-season verification conditions.
func (accumulator *searchProofAccumulator) observeSeason(observation releases.Observation, fixture searchFixture, titleExact, identityObserved, identityConflicting bool) bool {
	season, seasonObserved, inferred := observationSeason(observation)
	seasonExact := false
	if seasonObserved && season == fixture.season {
		seasonExact = true
		accumulator.seasonMatch = searchSeasonExact
		if inferred {
			accumulator.seasonToken = true
		}
	} else if seasonObserved {
		accumulator.seasonMatch = searchSeasonConflicting
		if identityObserved || titleExact {
			accumulator.addConflict("season_conflict")
		}
	}
	basis := observationPackBasis(observation)
	if basis != searchPackNone && accumulator.packBasis == searchPackNone {
		accumulator.packBasis = basis
	}
	if observationIsIndividualEpisode(observation) {
		accumulator.individual++
		if (identityObserved || titleExact) && seasonExact {
			accumulator.individualPlausible++
		}
	}
	if identityConflicting {
		return false
	}
	if (identityObserved || titleExact) && seasonExact && basis != searchPackNone {
		return true
	}
	return false
}

func observationIdentity(observation releases.Observation, fixture searchFixture) (bool, bool) {
	if fixture.identityNamespace == "" || fixture.identityValue == "" {
		return false, false
	}
	values, state := observationFactValues(observation, fixture.identityNamespace)
	if state == releases.FactConflicting {
		return false, true
	}
	for _, value := range values {
		if value == fixture.identityValue {
			return true, false
		}
	}
	return false, false
}

func observationSeason(observation releases.Observation) (uint16, bool, bool) {
	values, state := observationFactValues(observation, "season_number")
	if state == releases.FactConflicting {
		return 0, false, false
	}
	inferred := state == releases.FactInferred
	for _, value := range values {
		if parsed, ok := parseBoundedUint16(value); ok {
			return parsed, true, inferred
		}
	}
	return 0, false, false
}

func observationPackBasis(observation releases.Observation) string {
	for _, fact := range observation.Facts {
		if fact.Name != "release_scope" {
			continue
		}
		for _, value := range fact.Values {
			if value != "season_pack" {
				continue
			}
			if fact.State == releases.FactObserved {
				return searchPackProviderField
			}
			return searchPackTitleGrammar
		}
	}
	return searchPackNone
}

func observationIsIndividualEpisode(observation releases.Observation) bool {
	for _, fact := range observation.Facts {
		if fact.Name != "release_scope" || fact.State == releases.FactConflicting {
			continue
		}
		for _, value := range fact.Values {
			if value == "episode" {
				return true
			}
		}
	}
	return false
}

func observationFactValues(observation releases.Observation, name string) ([]string, releases.FactState) {
	state := releases.FactUnknown
	var values []string
	for _, fact := range observation.Facts {
		if fact.Name != name {
			continue
		}
		if fact.State == releases.FactConflicting {
			return nil, releases.FactConflicting
		}
		if fact.State == releases.FactObserved {
			state = releases.FactObserved
		} else if state != releases.FactObserved {
			state = fact.State
		}
		values = append(values, fact.Values...)
	}
	return values, state
}

func parseBoundedUint16(value string) (uint16, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 5 {
		return 0, false
	}
	parsed := 0
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
		parsed = parsed*10 + int(character-'0')
	}
	if parsed <= 0 || parsed > 9999 {
		return 0, false
	}
	return uint16(parsed), true
}

// searchFixture is the exact owner-trusted fixture identity used to interpret
// one search probe. It carries no provider or secret material.
type searchFixture struct {
	scope             newznab.SearchScope
	title             string
	year              *uint16
	season            uint16
	identityNamespace string
	identityValue     string
	requireIdentity   bool
}

// searchProbeVerdict maps the closed proof to the canonical ResponseVerdict
// and reports whether that verdict carries the closed proof as evidence.
// Unrelated results never invalidate an independently plausible qualifying
// result; any conflicting evidence is contradicted; semantic ambiguity never
// carries a proof.
func searchProbeVerdict(proof SearchProof) (ResponseVerdict, bool) {
	switch proof.ProviderOutcome {
	case searchProviderUnsupported:
		return ResponseUnsupported, true
	case searchProviderPermissionRestricted:
		return ResponsePermissionRestricted, true
	}
	switch {
	case proof.RawResultCount == 0:
		return ResponseNoResults, true
	case len(proof.ConflictCodes) > 0:
		return ResponseContradicted, true
	case proof.PlausibleIdentityCount > 0:
		return ResponseRecipeVerified, true
	case proof.RawResultCount > 0:
		return ResponseNoPlausibleMatch, true
	default:
		return ResponseSemanticAmbiguity, false
	}
}
