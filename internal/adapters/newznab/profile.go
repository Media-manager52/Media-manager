package newznab

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	CapabilityProfileSchemaVersion uint16 = 1
	MaxPrimaryRecipesPerScope             = 6
	MaxFallbackRecipesPerScope            = 2
	MaxVerifiedRequestLimit        uint16 = 100

	maxCapabilityProfileBytes  = 1 << 20
	maxCapabilityEvidence      = 128
	maxEvidenceKeysPerRecipe   = 16
	maxCategoryIDs             = 128
	maxCAPSCategoryNodes       = 4096
	maxCAPSCategoryDepth       = 8
	maxCAPSParameterNames      = 256
	maxCommissionResultCount   = 250_000
	maxProfileTextBytes        = 500
	maxServerVersionBytes      = 100
	maxEvidenceSummaryBytes    = 500
	maxProfileTextDecodePasses = 3
)

var (
	ErrInvalidCapabilityProfile     = errors.New("invalid Newznab capability profile")
	ErrCapabilityProfileNotActive   = errors.New("Newznab capability profile is not activatable")
	profileKeyPattern               = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,99}$`)
	evidenceKeyPattern              = regexp.MustCompile(`^evidence\.[a-f0-9]{32}$`)
	profileDigestPattern            = regexp.MustCompile(`^[a-f0-9]{64}$`)
	advertisedParameterNamePattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,63}$`)
	uriSchemePattern                = regexp.MustCompile(`(?i)(?:^|[^a-z0-9+.-])[a-z][a-z0-9+.-]*:[^\s]`)
	credentialParameterPattern      = regexp.MustCompile(`(?i)(?:api[_-]?key|access[_-]?token|token|password|authorization)\s*=`)
	credentialQueryNamePattern      = regexp.MustCompile(`(?i)[?&#;]\s*(?:api[_-]?key|access[_-]?token|token|password|authorization)\s*(?:=|[&#;]|$)`)
	credentialHeaderPattern         = regexp.MustCompile(`(?i)(?:authorization|proxy-authorization|cookie|set-cookie|x-api-key)\s*:`)
	authorizationValuePattern       = regexp.MustCompile(`(?i)(?:^|[^a-z0-9_-])(?:basic|bearer)\s+[a-z0-9._~+/=-]+(?:$|[^a-z0-9._~+/=-])`)
	protocolRelativeUserinfoPattern = regexp.MustCompile(`(?i)(?:^|[\s("'=])//[^/\s?#]*@`)
	schemelessUserinfoPattern       = regexp.MustCompile(`(?i)(?:^|[\s("'=])[^/\s:@]+:[^/\s@]+@(?:\[[0-9a-f:]+\]|[a-z0-9.-]+)(?::[0-9]+)?(?:[/\s?#]|$)`)
)

type SearchScope string

const (
	SearchScopeMovie          SearchScope = "movie"
	SearchScopeCompleteSeason SearchScope = "complete_season"
)

type SearchMode string

const (
	SearchModeGeneral SearchMode = "search"
	SearchModeMovie   SearchMode = "movie"
	SearchModeTV      SearchMode = "tvsearch"
)

type QueryStyle string

const (
	QueryNone             QueryStyle = "none"
	QueryTitle            QueryStyle = "title"
	QueryTitleYear        QueryStyle = "title_year"
	QueryTitleSeasonToken QueryStyle = "title_season_token"
)

type QuerySpec struct {
	Style QueryStyle `json:"style"`
}

type WireParameter string

const (
	WireQuery    WireParameter = "q"
	WireIMDbID   WireParameter = "imdbid"
	WireTMDbID   WireParameter = "tmdbid"
	WireTVDbID   WireParameter = "tvdbid"
	WireTVMazeID WireParameter = "tvmazeid"
	WireYear     WireParameter = "year"
	WireSeason   WireParameter = "season"
	WireEpisode  WireParameter = "ep"
	WireExtended WireParameter = "extended"
)

type BindingSource string

const (
	BindingIMDbNumeric   BindingSource = "imdb_numeric"
	BindingTMDbNumeric   BindingSource = "tmdb_numeric"
	BindingTVDbNumeric   BindingSource = "tvdb_numeric"
	BindingTVMazeNumeric BindingSource = "tvmaze_numeric"
	BindingYearNumber    BindingSource = "year_number"
	BindingSeasonNumber  BindingSource = "season_number"
)

type WireBinding struct {
	Parameter WireParameter `json:"parameter"`
	Source    BindingSource `json:"source"`
}

type CategorySetRef string

const (
	CategoryNone           CategorySetRef = "none"
	CategorySelectedMovie  CategorySetRef = "selected_movie"
	CategorySelectedSeries CategorySetRef = "selected_series"
	CategoryVerifiedFixed  CategorySetRef = "verified_fixed"
)

type CategorySpec struct {
	Set      CategorySetRef `json:"set"`
	FixedIDs []int          `json:"fixed_ids,omitempty"`
}

// NewznabCategorySelection is owner configuration, not capability evidence.
// Recipes refer to one of these sets rather than copying ordinary category IDs
// into an immutable profile.
type NewznabCategorySelection struct {
	Movie          []int `json:"movie_category_ids"`
	Series         []int `json:"series_category_ids"`
	ReviewRequired bool  `json:"category_selection_review_required"`
}

type RecipeRole string

const (
	RecipePrimary  RecipeRole = "primary"
	RecipeFallback RecipeRole = "fallback"
)

type FallbackTrigger string

const (
	FallbackZeroRawResults             FallbackTrigger = "zero_raw_results"
	FallbackNoPlausibleIdentityScope   FallbackTrigger = "no_plausible_identity_scope"
	FallbackDeterministicContradiction FallbackTrigger = "deterministic_recipe_contradiction"
)

type VerificationState string

const (
	VerificationUntested             VerificationState = "untested"
	VerificationVerified             VerificationState = "verified"
	VerificationUnsupported          VerificationState = "unsupported"
	VerificationPermissionRestricted VerificationState = "permission_restricted"
	VerificationContradicted         VerificationState = "contradicted"
)

type RecipeVerification struct {
	State        VerificationState `json:"state"`
	EvidenceKeys []string          `json:"evidence_keys,omitempty"`
}

// SearchRecipe is deliberately closed-world. Query controls q, Bindings are
// all required, Extended controls extended=1, and the shared client owns all
// structural and authentication parameters. V1 is XML-only and single-page.
type SearchRecipe struct {
	Key           string             `json:"key"`
	Scope         SearchScope        `json:"scope"`
	Mode          SearchMode         `json:"mode"`
	Query         QuerySpec          `json:"query"`
	Bindings      []WireBinding      `json:"bindings,omitempty"`
	Extended      bool               `json:"extended"`
	Categories    CategorySpec       `json:"categories"`
	VerifiedLimit uint16             `json:"verified_limit"`
	Role          RecipeRole         `json:"role"`
	Priority      uint8              `json:"priority"`
	FallbackOn    []FallbackTrigger  `json:"fallback_on,omitempty"`
	Verification  RecipeVerification `json:"verification"`
}

type CAPSElementState string

const (
	CAPSElementAbsent                 CAPSElementState = "absent"
	CAPSElementPresentAvailable       CAPSElementState = "present_available"
	CAPSElementPresentUnavailable     CAPSElementState = "present_unavailable"
	CAPSElementPresentUninterpretable CAPSElementState = "present_uninterpretable"
)

// CAPSModeObservation preserves the provider's bounded advertised names while
// exposing only a closed recognized subset to profile construction.
type CAPSModeObservation struct {
	Mode                     SearchMode       `json:"mode"`
	ElementState             CAPSElementState `json:"element_state"`
	Repeated                 bool             `json:"repeated,omitempty"`
	AdvertisedParameterNames []string         `json:"advertised_parameter_names,omitempty"`
	RecognizedParameters     []WireParameter  `json:"recognized_parameters,omitempty"`
}

type Operation string

const (
	OperationCAPS     Operation = "caps"
	OperationSearch   Operation = "search"
	OperationDetails  Operation = "details"
	OperationNFO      Operation = "nfo"
	OperationFileList Operation = "file_list"
	OperationFeed     Operation = "feed"
	OperationRetrieve Operation = "retrieve"
)

type ClaimState string

const (
	ClaimAdvertised ClaimState = "advertised"
	ClaimOmitted    ClaimState = "omitted"
	ClaimUnknown    ClaimState = "unknown"
)

type CapabilityAssessment struct {
	CAPSClaim     ClaimState        `json:"caps_claim,omitempty"`
	ProviderClaim ClaimState        `json:"provider_claim,omitempty"`
	Verification  VerificationState `json:"verification"`
	EvidenceKeys  []string          `json:"evidence_keys,omitempty"`
}

type ProviderContractRef struct {
	Key     string `json:"key"`
	Version uint16 `json:"version"`
}

const (
	ProviderContractNZBFinderV1    = "nzbfinder"
	ProviderContractPath32GetNZBV1 = "path32.getnzb"
	ProviderContractDrunkenSlugV1  = "drunkenslug"
)

type MappingKey string

const MappingNewznabSearchV1 MappingKey = "newznab.search"

type MappingRef struct {
	Key     MappingKey `json:"key"`
	Version uint16     `json:"version"`
}

type CAPSRecord struct {
	BodySHA256       string                `json:"body_sha256,omitempty"`
	ObservedAt       time.Time             `json:"observed_at,omitempty"`
	ServerTitle      string                `json:"server_title,omitempty"`
	ServerVersion    string                `json:"server_version,omitempty"`
	DefaultLimit     int                   `json:"default_limit,omitempty"`
	MaximumLimit     int                   `json:"maximum_limit,omitempty"`
	Categories       []Category            `json:"categories,omitempty"`
	ModeObservations []CAPSModeObservation `json:"mode_observations,omitempty"`
}

type OperationCapability struct {
	Operation  Operation            `json:"operation"`
	Assessment CapabilityAssessment `json:"assessment"`
	Contract   *ProviderContractRef `json:"contract,omitempty"`
}

type NativeIdentityMethod string

const (
	NativeIdentityGUID            NativeIdentityMethod = "native_guid"
	NativeIdentityDetailsContract NativeIdentityMethod = "details_permalink_contract"
)

type NativeIdentityCapability struct {
	Assessment CapabilityAssessment `json:"assessment"`
	Method     NativeIdentityMethod `json:"method,omitempty"`
	Contract   *ProviderContractRef `json:"contract,omitempty"`
}

type AdvertisedLocatorCapability struct {
	Assessment CapabilityAssessment `json:"assessment"`
	Validator  ProviderContractRef  `json:"validator"`
}

type DirectDownloadCapability struct {
	Assessment CapabilityAssessment `json:"assessment"`
	Contract   ProviderContractRef  `json:"contract"`
}

type RetrievalMethod string

const (
	RetrievalStandardGet       RetrievalMethod = "standard_get"
	RetrievalAdvertisedLocator RetrievalMethod = "advertised_locator"
	RetrievalProviderDirect    RetrievalMethod = "provider_direct"
)

type RetrievalProfile struct {
	NativeIdentity    NativeIdentityCapability     `json:"native_identity,omitempty"`
	StandardGet       CapabilityAssessment         `json:"standard_get,omitempty"`
	AdvertisedLocator *AdvertisedLocatorCapability `json:"advertised_locator,omitempty"`
	DirectDownload    *DirectDownloadCapability    `json:"direct_download,omitempty"`
	PreferredMethod   RetrievalMethod              `json:"preferred_method,omitempty"`
}

// EvidenceTargetKind identifies exactly one capability tested by a controlled
// commissioning observation. Search evidence additionally names its recipe;
// all other targets are closed operation or retrieval declarations.
type EvidenceTargetKind string

const (
	EvidenceTargetSearchRecipe      EvidenceTargetKind = "search_recipe"
	EvidenceTargetCAPS              EvidenceTargetKind = "caps"
	EvidenceTargetDetails           EvidenceTargetKind = "details"
	EvidenceTargetNFO               EvidenceTargetKind = "nfo"
	EvidenceTargetFileList          EvidenceTargetKind = "file_list"
	EvidenceTargetFeed              EvidenceTargetKind = "feed"
	EvidenceTargetNativeIdentity    EvidenceTargetKind = "native_identity"
	EvidenceTargetStandardGet       EvidenceTargetKind = "standard_get"
	EvidenceTargetAdvertisedLocator EvidenceTargetKind = "advertised_locator"
	EvidenceTargetProviderDirect    EvidenceTargetKind = "provider_direct"
)

type EvidenceTarget struct {
	Kind      EvidenceTargetKind `json:"kind"`
	RecipeKey string             `json:"recipe_key,omitempty"`
}

// EvidenceKind is intentionally small in schema v1. Future commissioning may
// add another reviewed kind, but provider-controlled text cannot define one.
type EvidenceKind string

const EvidenceKindControlledProbe EvidenceKind = "controlled_probe"

type EvidenceOutcome string

const (
	EvidenceOutcomeSearchSuccess        EvidenceOutcome = "success"
	EvidenceOutcomeNoResults            EvidenceOutcome = "no_results"
	EvidenceOutcomeSemanticallyVerified EvidenceOutcome = "verified"
	EvidenceOutcomeUnsupported          EvidenceOutcome = "unsupported"
	EvidenceOutcomePermissionRestricted EvidenceOutcome = "permission_restricted"
	EvidenceOutcomeContradicted         EvidenceOutcome = "contradicted"
)

// NewCommissionEvidenceKey creates the only supported evidence identifier
// shape. Future commissioning constructs keys locally; provider values are
// never used as identifiers.
func NewCommissionEvidenceKey() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("generate Newznab commissioning evidence key")
	}
	return "evidence." + hex.EncodeToString(random[:]), nil
}

type CommissionEvidence struct {
	Key                    string          `json:"key"`
	Kind                   EvidenceKind    `json:"kind"`
	Target                 EvidenceTarget  `json:"target"`
	ObservedAt             time.Time       `json:"observed_at,omitempty"`
	BodySHA256             string          `json:"body_sha256,omitempty"`
	SafeOutcome            EvidenceOutcome `json:"safe_outcome"`
	SafeSummary            string          `json:"safe_summary,omitempty"`
	RawResultCount         int             `json:"raw_result_count,omitempty"`
	PlausibleIdentityCount int             `json:"plausible_identity_count,omitempty"`
}

type CapabilityProfile struct {
	SchemaVersion    uint16                `json:"schema_version"`
	ProviderContract *ProviderContractRef  `json:"provider_contract,omitempty"`
	CAPS             CAPSRecord            `json:"caps"`
	SearchRecipes    []SearchRecipe        `json:"search_recipes"`
	Operations       []OperationCapability `json:"operations"`
	Retrieval        RetrievalProfile      `json:"retrieval"`
	MappingRefs      []MappingRef          `json:"mapping_refs"`
	Evidence         []CommissionEvidence  `json:"evidence"`
}

// DecodeCapabilityProfile rejects unknown JSON fields so credentials, URLs,
// templates, or other unowned data cannot hide inside an accepted document.
func DecodeCapabilityProfile(document []byte) (CapabilityProfile, error) {
	if len(document) == 0 || len(document) > maxCapabilityProfileBytes || !utf8.Valid(document) {
		return CapabilityProfile{}, ErrInvalidCapabilityProfile
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var profile CapabilityProfile
	if err := decoder.Decode(&profile); err != nil {
		return CapabilityProfile{}, ErrInvalidCapabilityProfile
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return CapabilityProfile{}, ErrInvalidCapabilityProfile
	}
	if err := ValidateCapabilityProfile(profile); err != nil {
		return CapabilityProfile{}, err
	}
	return profile, nil
}

// ValidateCapabilityProfile accepts truthful empty or untested draft profiles.
// Activation applies the stronger ValidateCapabilityProfileForActivation gate.
func ValidateCapabilityProfile(profile CapabilityProfile) error {
	if profile.SchemaVersion != CapabilityProfileSchemaVersion || len(profile.Evidence) > maxCapabilityEvidence {
		return ErrInvalidCapabilityProfile
	}
	evidence := make(map[string]CommissionEvidence, len(profile.Evidence))
	for _, item := range profile.Evidence {
		if !evidenceKeyPattern.MatchString(item.Key) || !validEvidenceKind(item.Kind) || !validEvidenceTarget(item.Target) || !validEvidenceOutcomeForTarget(item.Target, item.SafeOutcome) || !validProfileText(item.SafeSummary, maxEvidenceSummaryBytes) || item.ObservedAt.IsZero() || item.RawResultCount < 0 || item.RawResultCount > maxCommissionResultCount || item.PlausibleIdentityCount < 0 || item.PlausibleIdentityCount > item.RawResultCount || item.BodySHA256 != "" && !profileDigestPattern.MatchString(item.BodySHA256) {
			return ErrInvalidCapabilityProfile
		}
		if item.SafeOutcome == EvidenceOutcomeNoResults && (item.RawResultCount != 0 || item.PlausibleIdentityCount != 0) || item.SafeOutcome == EvidenceOutcomeSearchSuccess && item.RawResultCount == 0 {
			return ErrInvalidCapabilityProfile
		}
		if item.Target.Kind != EvidenceTargetSearchRecipe && (item.RawResultCount != 0 || item.PlausibleIdentityCount != 0) {
			return ErrInvalidCapabilityProfile
		}
		if _, duplicate := evidence[item.Key]; duplicate {
			return ErrInvalidCapabilityProfile
		}
		evidence[item.Key] = item
	}
	if err := validateCAPSRecord(profile.CAPS); err != nil {
		return err
	}
	if profile.ProviderContract != nil && !validContractRef(*profile.ProviderContract) {
		return ErrInvalidCapabilityProfile
	}
	keys := make(map[string]struct{}, len(profile.SearchRecipes))
	verifiedPrimaryScopes := make(map[SearchScope]struct{}, 2)
	counts := make(map[string]int, 4)
	for _, recipe := range profile.SearchRecipes {
		if err := validateSearchRecipe(recipe, profile.CAPS, evidence); err != nil {
			return err
		}
		if _, duplicate := keys[recipe.Key]; duplicate {
			return ErrInvalidCapabilityProfile
		}
		keys[recipe.Key] = struct{}{}
		countKey := string(recipe.Scope) + "\x00" + string(recipe.Role)
		counts[countKey]++
		if recipe.Role == RecipePrimary && counts[countKey] > MaxPrimaryRecipesPerScope || recipe.Role == RecipeFallback && counts[countKey] > MaxFallbackRecipesPerScope {
			return ErrInvalidCapabilityProfile
		}
		if recipe.Role == RecipePrimary && recipe.Verification.State == VerificationVerified {
			verifiedPrimaryScopes[recipe.Scope] = struct{}{}
		}
	}
	for _, recipe := range profile.SearchRecipes {
		if recipe.Role == RecipeFallback && recipe.Verification.State == VerificationVerified {
			if _, exists := verifiedPrimaryScopes[recipe.Scope]; !exists {
				return ErrInvalidCapabilityProfile
			}
		}
	}
	for _, item := range profile.Evidence {
		if item.Target.Kind == EvidenceTargetSearchRecipe {
			if _, exists := keys[item.Target.RecipeKey]; !exists {
				return ErrInvalidCapabilityProfile
			}
		}
	}
	operations := make(map[Operation]struct{}, len(profile.Operations))
	for _, capability := range profile.Operations {
		target, hasExactTarget := evidenceTargetForOperation(capability.Operation)
		if !validOperation(capability.Operation) || !validAssessment(capability.Assessment, evidence, target, hasExactTarget) || !validNestedContractRef(profile.ProviderContract, capability.Contract) {
			return ErrInvalidCapabilityProfile
		}
		if capability.Operation != OperationSearch && capability.Assessment.CAPSClaim != "" {
			return ErrInvalidCapabilityProfile
		}
		if _, duplicate := operations[capability.Operation]; duplicate {
			return ErrInvalidCapabilityProfile
		}
		operations[capability.Operation] = struct{}{}
	}
	mappings := make(map[MappingRef]struct{}, len(profile.MappingRefs))
	for _, mapping := range profile.MappingRefs {
		if !validMappingRef(mapping) {
			return ErrInvalidCapabilityProfile
		}
		if _, duplicate := mappings[mapping]; duplicate {
			return ErrInvalidCapabilityProfile
		}
		mappings[mapping] = struct{}{}
	}
	if !validRetrievalProfile(profile.Retrieval, evidence, profile.ProviderContract) {
		return ErrInvalidCapabilityProfile
	}
	return nil
}

func ValidateCapabilityProfileForActivation(profile CapabilityProfile) error {
	if err := ValidateCapabilityProfile(profile); err != nil {
		return err
	}
	for _, recipe := range profile.SearchRecipes {
		if recipe.Role == RecipePrimary && recipe.Verification.State == VerificationVerified {
			return nil
		}
	}
	return ErrCapabilityProfileNotActive
}

func validateSearchRecipe(recipe SearchRecipe, caps CAPSRecord, evidence map[string]CommissionEvidence) error {
	if !validProfileKey(recipe.Key) || !validSearchScope(recipe.Scope) || !validSearchMode(recipe.Mode) || !validQueryStyle(recipe.Query.Style) || !validRecipeRole(recipe.Role) || !validVerificationState(recipe.Verification.State) || recipe.VerifiedLimit == 0 || recipe.VerifiedLimit > MaxVerifiedRequestLimit || caps.MaximumLimit > 0 && int(recipe.VerifiedLimit) > caps.MaximumLimit || len(recipe.Verification.EvidenceKeys) > maxEvidenceKeysPerRecipe {
		return ErrInvalidCapabilityProfile
	}
	if recipe.Scope == SearchScopeMovie && recipe.Mode == SearchModeTV || recipe.Scope == SearchScopeCompleteSeason && recipe.Mode == SearchModeMovie || recipe.Scope == SearchScopeMovie && recipe.Query.Style == QueryTitleSeasonToken {
		return ErrInvalidCapabilityProfile
	}
	evidenceSeen := make(map[string]struct{}, len(recipe.Verification.EvidenceKeys))
	if recipe.Verification.State == VerificationUntested {
		if len(recipe.Verification.EvidenceKeys) != 0 {
			return ErrInvalidCapabilityProfile
		}
	} else if len(recipe.Verification.EvidenceKeys) == 0 {
		return ErrInvalidCapabilityProfile
	}
	verificationSupported := recipe.Verification.State == VerificationUntested
	for _, key := range recipe.Verification.EvidenceKeys {
		if !evidenceKeyPattern.MatchString(key) {
			return ErrInvalidCapabilityProfile
		}
		if _, duplicate := evidenceSeen[key]; duplicate {
			return ErrInvalidCapabilityProfile
		}
		item, exists := evidence[key]
		if !exists {
			return ErrInvalidCapabilityProfile
		}
		if item.Target != (EvidenceTarget{Kind: EvidenceTargetSearchRecipe, RecipeKey: recipe.Key}) {
			return ErrInvalidCapabilityProfile
		}
		if !searchEvidenceSupportsVerification(recipe.Verification.State, recipe.Key, item) {
			return ErrInvalidCapabilityProfile
		}
		verificationSupported = true
		evidenceSeen[key] = struct{}{}
	}
	if !verificationSupported {
		return ErrInvalidCapabilityProfile
	}
	parameters := make(map[WireParameter]struct{}, len(recipe.Bindings))
	sources := make(map[BindingSource]struct{}, len(recipe.Bindings))
	hasIdentity, hasSeason := false, recipe.Query.Style == QueryTitleSeasonToken
	for _, binding := range recipe.Bindings {
		if !validBinding(binding) {
			return ErrInvalidCapabilityProfile
		}
		if _, duplicate := parameters[binding.Parameter]; duplicate {
			return ErrInvalidCapabilityProfile
		}
		if _, duplicate := sources[binding.Source]; duplicate {
			return ErrInvalidCapabilityProfile
		}
		parameters[binding.Parameter] = struct{}{}
		sources[binding.Source] = struct{}{}
		switch binding.Parameter {
		case WireIMDbID, WireTMDbID, WireTVDbID, WireTVMazeID:
			hasIdentity = true
		case WireSeason:
			hasSeason = true
		}
	}
	if recipe.Query.Style == QueryNone && !hasIdentity || recipe.Scope == SearchScopeCompleteSeason && !hasSeason || recipe.Scope == SearchScopeMovie && hasSeason {
		return ErrInvalidCapabilityProfile
	}
	if !validCategorySpec(recipe.Scope, recipe.Categories, caps.Categories) {
		return ErrInvalidCapabilityProfile
	}
	if recipe.Role == RecipePrimary && len(recipe.FallbackOn) != 0 || recipe.Role == RecipeFallback && len(recipe.FallbackOn) == 0 {
		return ErrInvalidCapabilityProfile
	}
	fallbacks := make(map[FallbackTrigger]struct{}, len(recipe.FallbackOn))
	for _, trigger := range recipe.FallbackOn {
		if !ValidFallbackTrigger(trigger) {
			return ErrInvalidCapabilityProfile
		}
		if _, duplicate := fallbacks[trigger]; duplicate {
			return ErrInvalidCapabilityProfile
		}
		fallbacks[trigger] = struct{}{}
	}
	return nil
}

func ValidFallbackTrigger(trigger FallbackTrigger) bool {
	switch trigger {
	case FallbackZeroRawResults, FallbackNoPlausibleIdentityScope, FallbackDeterministicContradiction:
		return true
	default:
		return false
	}
}

func validBinding(binding WireBinding) bool {
	switch binding.Parameter {
	case WireIMDbID:
		return binding.Source == BindingIMDbNumeric
	case WireTMDbID:
		return binding.Source == BindingTMDbNumeric
	case WireTVDbID:
		return binding.Source == BindingTVDbNumeric
	case WireTVMazeID:
		return binding.Source == BindingTVMazeNumeric
	case WireYear:
		return binding.Source == BindingYearNumber
	case WireSeason:
		return binding.Source == BindingSeasonNumber
	default:
		return false
	}
}

func validCategorySpec(scope SearchScope, specification CategorySpec, available []Category) bool {
	switch specification.Set {
	case CategoryNone:
		return len(specification.FixedIDs) == 0
	case CategorySelectedMovie:
		return scope == SearchScopeMovie && len(specification.FixedIDs) == 0
	case CategorySelectedSeries:
		return scope == SearchScopeCompleteSeason && len(specification.FixedIDs) == 0
	case CategoryVerifiedFixed:
		return validCategoryIDs(specification.FixedIDs, available, true)
	default:
		return false
	}
}

func validCategoryIDs(values []int, available []Category, requireNonempty bool) bool {
	if requireNonempty && len(values) == 0 || len(values) > maxCategoryIDs {
		return false
	}
	availableIDs := make(map[int]struct{})
	collectCategoryIDs(available, availableIDs)
	previous := 0
	for index, value := range values {
		if value <= 0 || index > 0 && value <= previous {
			return false
		}
		if _, exists := availableIDs[value]; !exists {
			return false
		}
		previous = value
	}
	return true
}

func collectCategoryIDs(categories []Category, result map[int]struct{}) {
	for _, category := range categories {
		result[category.ID] = struct{}{}
		collectCategoryIDs(category.Children, result)
	}
}

func validateCAPSRecord(record CAPSRecord) error {
	hasCAPSEvidence := record.BodySHA256 != "" || !record.ObservedAt.IsZero() || record.ServerTitle != "" || record.ServerVersion != "" || record.DefaultLimit != 0 || record.MaximumLimit != 0 || len(record.Categories) != 0 || len(record.ModeObservations) != 0
	if hasCAPSEvidence && (!profileDigestPattern.MatchString(record.BodySHA256) || record.ObservedAt.IsZero()) || record.DefaultLimit < 0 || record.MaximumLimit < 0 || record.MaximumLimit > 0 && record.DefaultLimit > record.MaximumLimit || !validProfileText(record.ServerTitle, maxProfileTextBytes) || !validProfileText(record.ServerVersion, maxServerVersionBytes) {
		return ErrInvalidCapabilityProfile
	}
	categoryIDs := make(map[int]struct{})
	nodes := 0
	if !validateCAPSCategories(record.Categories, 0, &nodes, categoryIDs) {
		return ErrInvalidCapabilityProfile
	}
	modes := make(map[SearchMode]struct{}, len(record.ModeObservations))
	for _, observation := range record.ModeObservations {
		if !validSearchMode(observation.Mode) || !validCAPSElementState(observation.ElementState) || len(observation.AdvertisedParameterNames) > maxCAPSParameterNames || len(observation.RecognizedParameters) > len(observation.AdvertisedParameterNames) {
			return ErrInvalidCapabilityProfile
		}
		if _, duplicate := modes[observation.Mode]; duplicate {
			return ErrInvalidCapabilityProfile
		}
		modes[observation.Mode] = struct{}{}
		if observation.ElementState == CAPSElementAbsent && (len(observation.AdvertisedParameterNames) != 0 || len(observation.RecognizedParameters) != 0) {
			return ErrInvalidCapabilityProfile
		}
		if observation.Repeated && observation.ElementState != CAPSElementPresentUninterpretable {
			return ErrInvalidCapabilityProfile
		}
		advertised := make(map[string]struct{}, len(observation.AdvertisedParameterNames))
		for _, name := range observation.AdvertisedParameterNames {
			if !advertisedParameterNamePattern.MatchString(name) {
				return ErrInvalidCapabilityProfile
			}
			advertised[strings.ToLower(name)] = struct{}{}
		}
		recognized := make(map[WireParameter]struct{}, len(observation.RecognizedParameters))
		for _, parameter := range observation.RecognizedParameters {
			if !validRecognizedWireParameter(parameter) {
				return ErrInvalidCapabilityProfile
			}
			if _, duplicate := recognized[parameter]; duplicate {
				return ErrInvalidCapabilityProfile
			}
			if _, present := advertised[string(parameter)]; !present {
				return ErrInvalidCapabilityProfile
			}
			recognized[parameter] = struct{}{}
		}
	}
	return nil
}

func validateCAPSCategories(categories []Category, depth int, nodes *int, ids map[int]struct{}) bool {
	if depth > maxCAPSCategoryDepth {
		return false
	}
	for _, category := range categories {
		*nodes = *nodes + 1
		if *nodes > maxCAPSCategoryNodes || category.ID <= 0 || !validProfileText(category.Name, maxProfileTextBytes) {
			return false
		}
		if _, duplicate := ids[category.ID]; duplicate {
			return false
		}
		ids[category.ID] = struct{}{}
		if !validateCAPSCategories(category.Children, depth+1, nodes, ids) {
			return false
		}
	}
	return true
}

func validAssessment(assessment CapabilityAssessment, evidence map[string]CommissionEvidence, target EvidenceTarget, hasExactTarget bool) bool {
	if !validOptionalClaimState(assessment.CAPSClaim) || !validOptionalClaimState(assessment.ProviderClaim) || !validVerificationState(assessment.Verification) || len(assessment.EvidenceKeys) > maxEvidenceKeysPerRecipe {
		return false
	}
	if assessment.Verification == VerificationUntested {
		return len(assessment.EvidenceKeys) == 0
	}
	if !hasExactTarget {
		return false
	}
	seen := make(map[string]struct{}, len(assessment.EvidenceKeys))
	verificationSupported := false
	for _, key := range assessment.EvidenceKeys {
		if !evidenceKeyPattern.MatchString(key) {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		item, exists := evidence[key]
		if !exists {
			return false
		}
		if item.Target != target {
			return false
		}
		if !assessmentEvidenceSupportsVerification(assessment.Verification, item) {
			return false
		}
		verificationSupported = true
		seen[key] = struct{}{}
	}
	return verificationSupported
}

func searchEvidenceSupportsVerification(state VerificationState, recipeKey string, evidence CommissionEvidence) bool {
	if evidence.Target != (EvidenceTarget{Kind: EvidenceTargetSearchRecipe, RecipeKey: recipeKey}) {
		return false
	}
	if state == VerificationVerified {
		return durableCommissionEvidence(evidence) && evidence.SafeOutcome == EvidenceOutcomeSearchSuccess && evidence.PlausibleIdentityCount > 0
	}
	return assessmentEvidenceSupportsVerification(state, evidence)
}

func assessmentEvidenceSupportsVerification(state VerificationState, evidence CommissionEvidence) bool {
	var want EvidenceOutcome
	switch state {
	case VerificationVerified:
		want = EvidenceOutcomeSemanticallyVerified
	case VerificationUnsupported:
		want = EvidenceOutcomeUnsupported
	case VerificationPermissionRestricted:
		want = EvidenceOutcomePermissionRestricted
	case VerificationContradicted:
		want = EvidenceOutcomeContradicted
	case VerificationUntested:
		return true
	default:
		return false
	}
	return durableCommissionEvidence(evidence) && evidence.SafeOutcome == want
}

func durableCommissionEvidence(evidence CommissionEvidence) bool {
	return !evidence.ObservedAt.IsZero() && profileDigestPattern.MatchString(evidence.BodySHA256)
}

func validRetrievalProfile(profile RetrievalProfile, evidence map[string]CommissionEvidence, selectedContract *ProviderContractRef) bool {
	zero := profile.NativeIdentity.Method == "" && profile.NativeIdentity.Contract == nil && zeroAssessment(profile.NativeIdentity.Assessment) && zeroAssessment(profile.StandardGet) && profile.AdvertisedLocator == nil && profile.DirectDownload == nil && profile.PreferredMethod == ""
	if zero {
		return true
	}
	if !validAssessment(profile.NativeIdentity.Assessment, evidence, EvidenceTarget{Kind: EvidenceTargetNativeIdentity}, true) || !validAssessment(profile.StandardGet, evidence, EvidenceTarget{Kind: EvidenceTargetStandardGet}, true) || profile.PreferredMethod != "" && !validRetrievalMethod(profile.PreferredMethod) {
		return false
	}
	if profile.NativeIdentity.Assessment.CAPSClaim != "" || profile.StandardGet.CAPSClaim != "" {
		return false
	}
	switch profile.NativeIdentity.Method {
	case NativeIdentityGUID:
		if profile.NativeIdentity.Contract != nil {
			return false
		}
	case NativeIdentityDetailsContract:
		if profile.NativeIdentity.Contract == nil || !validNestedContractRef(selectedContract, profile.NativeIdentity.Contract) {
			return false
		}
	default:
		return false
	}
	if profile.AdvertisedLocator != nil {
		if !validAssessment(profile.AdvertisedLocator.Assessment, evidence, EvidenceTarget{Kind: EvidenceTargetAdvertisedLocator}, true) || profile.AdvertisedLocator.Assessment.CAPSClaim != "" || !validNestedContractRef(selectedContract, &profile.AdvertisedLocator.Validator) {
			return false
		}
	}
	if profile.DirectDownload != nil {
		if !validAssessment(profile.DirectDownload.Assessment, evidence, EvidenceTarget{Kind: EvidenceTargetProviderDirect}, true) || profile.DirectDownload.Assessment.CAPSClaim != "" || !validNestedContractRef(selectedContract, &profile.DirectDownload.Contract) {
			return false
		}
	}
	if profile.PreferredMethod == "" {
		return true
	}
	if profile.NativeIdentity.Assessment.Verification != VerificationVerified {
		return false
	}
	switch profile.PreferredMethod {
	case RetrievalStandardGet:
		return profile.StandardGet.Verification == VerificationVerified
	case RetrievalAdvertisedLocator:
		return profile.AdvertisedLocator != nil && profile.AdvertisedLocator.Assessment.Verification == VerificationVerified
	case RetrievalProviderDirect:
		return profile.DirectDownload != nil && profile.DirectDownload.Assessment.Verification == VerificationVerified
	}
	return false
}

func zeroAssessment(assessment CapabilityAssessment) bool {
	return assessment.CAPSClaim == "" && assessment.ProviderClaim == "" && assessment.Verification == "" && len(assessment.EvidenceKeys) == 0
}

func validContractRef(reference ProviderContractRef) bool {
	// No profile-backed provider behavior is active in this milestone. Known
	// audited references are added here alongside their Go implementations.
	return reference.Version > 0 && validProfileKey(reference.Key) && knownProviderContract(reference)
}

func validNestedContractRef(selected, nested *ProviderContractRef) bool {
	return nested == nil || selected != nil && *nested == *selected && validContractRef(*nested)
}

func knownProviderContract(reference ProviderContractRef) bool {
	if reference.Version != 1 {
		return false
	}
	switch reference.Key {
	case ProviderContractNZBFinderV1, ProviderContractPath32GetNZBV1, ProviderContractDrunkenSlugV1:
		return true
	default:
		return false
	}
}

// ValidProviderContractKey lets owner-configuration validation share this
// closed registry without duplicating provider knowledge in PostgreSQL code.
func ValidProviderContractKey(key string) bool {
	return knownProviderContract(ProviderContractRef{Key: strings.TrimSpace(key), Version: 1})
}

// ProviderContractAcceptsEndpoint verifies the owner-selected contract against
// the small audited origin registry. It performs no request and does not infer
// a contract from provider-controlled data.
func ProviderContractAcceptsEndpoint(reference ProviderContractRef, endpoint string) bool {
	if !knownProviderContract(reference) {
		return false
	}
	parsed, err := url.ParseRequestURI(strings.TrimSpace(endpoint))
	if err != nil || !parsed.IsAbs() || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	origin, err := exactOrigin(parsed)
	if err != nil || origin.scheme != "https" || origin.port != "443" {
		return false
	}
	switch reference.Key {
	case ProviderContractNZBFinderV1:
		return origin.hostname == "nzbfinder.ws"
	case ProviderContractDrunkenSlugV1:
		return origin.hostname == "drunkenslug.com"
	case ProviderContractPath32GetNZBV1:
		switch origin.hostname {
		case "abnzb.com", "api.althub.co.za", "api.nzb.life", "msgnews.live", "ninjacentral.co.za":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func validMappingRef(reference MappingRef) bool {
	// Mapping references follow the same closed registration rule as provider
	// contracts. The legacy parser remains authoritative in this milestone.
	return reference.Version > 0 && validProfileKey(string(reference.Key)) && knownMapping(reference)
}

func knownMapping(reference MappingRef) bool {
	return reference.Key == MappingNewznabSearchV1 && reference.Version == 1
}

func validProfileKey(value string) bool { return profileKeyPattern.MatchString(value) }

// ValidStructurallySafePersistedText applies the capability profile's bounded
// URL/auth-material and control-character exclusions to another profile-bound
// document. It deliberately does not claim to recognize arbitrary secrets;
// future live commissioning must still compare against the resolved credential
// at its authorized construction boundary.
func ValidStructurallySafePersistedText(value string, maximumBytes int) bool {
	return maximumBytes > 0 && maximumBytes <= maxProfileTextBytes && validProfileText(value, maximumBytes)
}

func validProfileText(value string, maximum int) bool {
	current := value
	decodedPasses := 0
	for {
		if len(current) > maximum || !utf8.ValidString(current) || unsafeProfileText(current) {
			return false
		}
		for _, character := range current {
			if unicode.IsControl(character) {
				return false
			}
		}

		decoded, changed := decodeProfilePercentTriplets(current)
		if !changed {
			return true
		}
		if decodedPasses == maxProfileTextDecodePasses {
			return false
		}
		current = decoded
		decodedPasses++
	}
}

// decodeProfilePercentTriplets decodes only complete %HH byte triplets. A '%'
// that is not followed by two hexadecimal digits is ordinary literal text.
// This deliberately does not apply query-form semantics, so '+' remains '+'.
func decodeProfilePercentTriplets(value string) (string, bool) {
	first := strings.IndexByte(value, '%')
	if first < 0 {
		return value, false
	}

	var decoded strings.Builder
	decoded.Grow(len(value))
	decoded.WriteString(value[:first])
	changed := false
	for index := first; index < len(value); {
		if value[index] == '%' && index+2 < len(value) {
			high, highOK := profileHexNibble(value[index+1])
			low, lowOK := profileHexNibble(value[index+2])
			if highOK && lowOK {
				decoded.WriteByte(high<<4 | low)
				index += 3
				changed = true
				continue
			}
		}
		decoded.WriteByte(value[index])
		index++
	}
	if !changed {
		return value, false
	}
	return decoded.String(), true
}

func profileHexNibble(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func unsafeProfileText(value string) bool {
	return uriSchemePattern.MatchString(value) ||
		credentialParameterPattern.MatchString(value) ||
		credentialQueryNamePattern.MatchString(value) ||
		credentialHeaderPattern.MatchString(value) ||
		authorizationValuePattern.MatchString(value) ||
		protocolRelativeUserinfoPattern.MatchString(value) ||
		schemelessUserinfoPattern.MatchString(value)
}

func validEvidenceKind(kind EvidenceKind) bool {
	return kind == EvidenceKindControlledProbe
}

func validEvidenceOutcome(outcome EvidenceOutcome) bool {
	switch outcome {
	case EvidenceOutcomeSearchSuccess, EvidenceOutcomeNoResults, EvidenceOutcomeSemanticallyVerified, EvidenceOutcomeUnsupported, EvidenceOutcomePermissionRestricted, EvidenceOutcomeContradicted:
		return true
	default:
		return false
	}
}

func validEvidenceOutcomeForTarget(target EvidenceTarget, outcome EvidenceOutcome) bool {
	if !validEvidenceOutcome(outcome) {
		return false
	}
	if target.Kind == EvidenceTargetSearchRecipe {
		return outcome != EvidenceOutcomeSemanticallyVerified
	}
	return outcome != EvidenceOutcomeSearchSuccess && outcome != EvidenceOutcomeNoResults
}

func validEvidenceTarget(target EvidenceTarget) bool {
	switch target.Kind {
	case EvidenceTargetSearchRecipe:
		return validProfileKey(target.RecipeKey)
	case EvidenceTargetCAPS, EvidenceTargetDetails, EvidenceTargetNFO, EvidenceTargetFileList, EvidenceTargetFeed, EvidenceTargetNativeIdentity, EvidenceTargetStandardGet, EvidenceTargetAdvertisedLocator, EvidenceTargetProviderDirect:
		return target.RecipeKey == ""
	default:
		return false
	}
}

func evidenceTargetForOperation(operation Operation) (EvidenceTarget, bool) {
	switch operation {
	case OperationCAPS:
		return EvidenceTarget{Kind: EvidenceTargetCAPS}, true
	case OperationDetails:
		return EvidenceTarget{Kind: EvidenceTargetDetails}, true
	case OperationNFO:
		return EvidenceTarget{Kind: EvidenceTargetNFO}, true
	case OperationFileList:
		return EvidenceTarget{Kind: EvidenceTargetFileList}, true
	case OperationFeed:
		return EvidenceTarget{Kind: EvidenceTargetFeed}, true
	default:
		// Search evidence is bound to one exact recipe, while retrieval
		// evidence is bound to one exact retrieval declaration. The generic
		// operation rows cannot truthfully stand in for either.
		return EvidenceTarget{}, false
	}
}

func validSearchScope(value SearchScope) bool {
	return value == SearchScopeMovie || value == SearchScopeCompleteSeason
}

func validSearchMode(value SearchMode) bool {
	return value == SearchModeGeneral || value == SearchModeMovie || value == SearchModeTV
}

func validQueryStyle(value QueryStyle) bool {
	switch value {
	case QueryNone, QueryTitle, QueryTitleYear, QueryTitleSeasonToken:
		return true
	default:
		return false
	}
}

func validRecipeRole(value RecipeRole) bool { return value == RecipePrimary || value == RecipeFallback }

func validVerificationState(value VerificationState) bool {
	switch value {
	case VerificationUntested, VerificationVerified, VerificationUnsupported, VerificationPermissionRestricted, VerificationContradicted:
		return true
	default:
		return false
	}
}

func validCAPSElementState(value CAPSElementState) bool {
	switch value {
	case CAPSElementAbsent, CAPSElementPresentAvailable, CAPSElementPresentUnavailable, CAPSElementPresentUninterpretable:
		return true
	default:
		return false
	}
}

func validRecognizedWireParameter(value WireParameter) bool {
	switch value {
	case WireQuery, WireIMDbID, WireTMDbID, WireTVDbID, WireTVMazeID, WireYear, WireSeason, WireEpisode, WireExtended:
		return true
	default:
		return false
	}
}

func validOperation(value Operation) bool {
	switch value {
	case OperationCAPS, OperationSearch, OperationDetails, OperationNFO, OperationFileList, OperationFeed, OperationRetrieve:
		return true
	default:
		return false
	}
}

// ValidOperation exposes the same closed operation registry to persistence
// validation without duplicating Newznab domain knowledge there.
func ValidOperation(value Operation) bool { return validOperation(value) }

func validClaimState(value ClaimState) bool {
	return value == ClaimAdvertised || value == ClaimOmitted || value == ClaimUnknown
}

func validOptionalClaimState(value ClaimState) bool {
	return value == "" || validClaimState(value)
}

func validRetrievalMethod(value RetrievalMethod) bool {
	return value == RetrievalStandardGet || value == RetrievalAdvertisedLocator || value == RetrievalProviderDirect
}
