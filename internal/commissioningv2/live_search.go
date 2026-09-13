package commissioningv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
)

const searchProbeLimitMax = 100

// materializeCommissioningProbe builds the exact bounded probe for one
// authorized step. CAPS is a fixed request. Search is materialized only from
// the immutable TypedRequest and the owner-trusted fixtures bound to the
// durable run; caller input can never add a parameter, recipe, or category.
func materializeCommissioningProbe(snapshot LiveAuthorizedStep) (newznab.CommissioningProbe, searchFixture, error) {
	switch snapshot.Phase {
	case StepPhaseCAPS:
		if snapshot.Target.Kind != newznab.EvidenceTargetCAPS {
			return newznab.CommissioningProbe{}, searchFixture{}, ErrLiveExecutorNotAuthorized
		}
		return newznab.CommissioningProbe{Mode: "caps"}, searchFixture{}, nil
	case StepPhaseSearch:
		return materializeSearchProbe(snapshot)
	default:
		return newznab.CommissioningProbe{}, searchFixture{}, ErrLiveExecutorNotAuthorized
	}
}

func materializeSearchProbe(snapshot LiveAuthorizedStep) (newznab.CommissioningProbe, searchFixture, error) {
	request := snapshot.Request
	if request.Operation != newznab.OperationSearch || request.Output != OutputXML || request.Pagination != PaginationNone {
		return newznab.CommissioningProbe{}, searchFixture{}, ErrInvalidCommissioningDocument
	}
	if snapshot.Target.Kind != newznab.EvidenceTargetSearchRecipe || !recipeKeyPattern.MatchString(snapshot.Target.RecipeKey) {
		return newznab.CommissioningProbe{}, searchFixture{}, ErrInvalidCommissioningDocument
	}
	limit := int(request.RequestLimit)
	if limit <= 0 || limit > searchProbeLimitMax {
		return newznab.CommissioningProbe{}, searchFixture{}, ErrInvalidCommissioningDocument
	}
	fixture, params, err := resolveSearchFixture(snapshot)
	if err != nil {
		return newznab.CommissioningProbe{}, searchFixture{}, err
	}
	probe := newznab.CommissioningProbe{
		Mode:       string(request.Mode),
		Params:     params,
		Categories: append([]int(nil), snapshot.CategoryIDs...),
		Limit:      limit,
		Extended:   request.Extended,
	}
	return probe, fixture, nil
}

func resolveSearchFixture(snapshot LiveAuthorizedStep) (searchFixture, []newznab.Param, error) {
	request := snapshot.Request
	identities := map[IdentityNamespace]string{}
	fixture := searchFixture{scope: snapshot.Scope, requireIdentity: request.Query.Style == newznab.QueryNone}
	switch snapshot.Scope {
	case newznab.SearchScopeMovie:
		movie := snapshot.Fixtures.Movie
		if movie == nil || movie.Key != snapshot.FixtureKey || strings.TrimSpace(movie.CanonicalTitle) == "" {
			return searchFixture{}, nil, ErrInvalidCommissioningDocument
		}
		fixture.title, fixture.year = movie.CanonicalTitle, movie.Year
		for _, identity := range movie.Identities {
			identities[identity.Namespace] = identity.Value
		}
	case newznab.SearchScopeCompleteSeason:
		season := snapshot.Fixtures.CompleteSeason
		if season == nil || season.Key != snapshot.FixtureKey || strings.TrimSpace(season.CanonicalTitle) == "" || season.Season == 0 {
			return searchFixture{}, nil, ErrInvalidCommissioningDocument
		}
		fixture.title, fixture.year, fixture.season = season.CanonicalTitle, season.Year, season.Season
		for _, identity := range season.Identities {
			identities[identity.Namespace] = identity.Value
		}
	default:
		return searchFixture{}, nil, ErrInvalidCommissioningDocument
	}
	var params []newznab.Param
	switch request.Query.Style {
	case newznab.QueryNone:
	case newznab.QueryTitle:
		params = append(params, newznab.Param{Name: string(newznab.WireQuery), Value: fixture.title})
	case newznab.QueryTitleYear:
		if fixture.year == nil {
			return searchFixture{}, nil, ErrInvalidCommissioningDocument
		}
		params = append(params, newznab.Param{Name: string(newznab.WireQuery), Value: fixture.title + " " + strconv.Itoa(int(*fixture.year))})
	case newznab.QueryTitleSeasonToken:
		if fixture.season == 0 {
			return searchFixture{}, nil, ErrInvalidCommissioningDocument
		}
		params = append(params, newznab.Param{Name: string(newznab.WireQuery), Value: fmt.Sprintf("%s S%02d", fixture.title, fixture.season)})
	default:
		return searchFixture{}, nil, ErrInvalidCommissioningDocument
	}
	for _, binding := range request.Bindings {
		value, namespace, ok := searchBindingValue(binding, identities, fixture)
		if !ok {
			return searchFixture{}, nil, ErrInvalidCommissioningDocument
		}
		if namespace != "" {
			fixture.identityNamespace = identityFactName(namespace)
			fixture.identityValue = value
		}
		params = append(params, newznab.Param{Name: string(binding.Parameter), Value: value})
	}
	if len(params) > 16 {
		return searchFixture{}, nil, ErrInvalidCommissioningDocument
	}
	return fixture, params, nil
}

func searchBindingValue(binding newznab.WireBinding, identities map[IdentityNamespace]string, fixture searchFixture) (string, IdentityNamespace, bool) {
	switch binding.Source {
	case newznab.BindingIMDbNumeric:
		return identities[IdentityIMDb], IdentityIMDb, identities[IdentityIMDb] != ""
	case newznab.BindingTMDbNumeric:
		return identities[IdentityTMDb], IdentityTMDb, identities[IdentityTMDb] != ""
	case newznab.BindingTVDbNumeric:
		return identities[IdentityTVDb], IdentityTVDb, identities[IdentityTVDb] != ""
	case newznab.BindingTVMazeNumeric:
		return identities[IdentityTVMaze], IdentityTVMaze, identities[IdentityTVMaze] != ""
	case newznab.BindingYearNumber:
		if fixture.year == nil {
			return "", "", false
		}
		return strconv.Itoa(int(*fixture.year)), "", true
	case newznab.BindingSeasonNumber:
		if fixture.season == 0 {
			return "", "", false
		}
		return strconv.Itoa(int(fixture.season)), "", true
	default:
		return "", "", false
	}
}

func identityFactName(namespace IdentityNamespace) string {
	switch namespace {
	case IdentityIMDb:
		return "imdb_id"
	case IdentityTMDb:
		return "tmdb_id"
	case IdentityTVDb:
		return "tvdb_id"
	case IdentityTVMaze:
		return "tvmaze_id"
	default:
		return ""
	}
}

// classifySearchAttemptInputs builds the closed classification input from one
// guarded search transmission. The search-specific classifier
// (ClassifySearchAttempt) consumes these inputs; the closed semantic proof is
// the only search evidence persisted.
func classifySearchAttemptInputs(snapshot LiveAuthorizedStep, result newznab.CommissioningResult, fetchErr error, observedAt time.Time, evidenceKey string) (LiveAttemptInputs, error) {
	if fetchErr != nil {
		return liveAttemptInputsFromError(fetchErr, snapshot.Target)
	}
	observedAt = observedAt.UTC().Truncate(time.Microsecond)
	observations, err := newznab.ParseSearch(result.Body, snapshot.IndexerID, string(snapshot.Request.Operation), observedAt)
	if err != nil {
		return searchProviderErrorInputs(snapshot, result, observedAt, evidenceKey, err)
	}
	fixture, _, err := resolveSearchFixture(snapshot)
	if err != nil {
		return LiveAttemptInputs{}, err
	}
	proof, err := BuildSearchProof(snapshot.Scope, fixture, observations)
	if err != nil {
		return LiveAttemptInputs{}, err
	}
	verdict, carriesEvidence := searchProbeVerdict(proof)
	if !carriesEvidence {
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: verdict, TargetKind: snapshot.Target}, nil
	}
	return searchEvidenceInputs(snapshot, result, observedAt, evidenceKey, verdict, proof)
}

// searchProviderErrorInputs maps a deterministic Newznab provider error to the
// closed outcome it proves. Only deterministic, closed protocol evidence is
// used: ProviderError.Unsupported (code 203 / "no such function" /
// "not supported") proves unsupported; provider code 102 (insufficient
// privileges) proves permission_restricted; provider codes 100/101
// (credentials/suspension) prove authentication failure. Anything else fails
// closed as malformed_response. Generic HTTP failures never reach here.
func searchProviderErrorInputs(snapshot LiveAuthorizedStep, result newznab.CommissioningResult, observedAt time.Time, evidenceKey string, parseErr error) (LiveAttemptInputs, error) {
	var provider newznab.ProviderError
	if !errors.As(parseErr, &provider) {
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseMalformed, TargetKind: snapshot.Target}, nil
	}
	switch {
	case provider.Unsupported:
		return searchProviderEvidenceInputs(snapshot, result, observedAt, evidenceKey, ResponseUnsupported, searchProviderUnsupported)
	case provider.Code == "102":
		return searchProviderEvidenceInputs(snapshot, result, observedAt, evidenceKey, ResponsePermissionRestricted, searchProviderPermissionRestricted)
	case provider.Code == "100" || provider.Code == "101":
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseAuthenticationFailure, TargetKind: snapshot.Target}, nil
	default:
		return LiveAttemptInputs{Transport: TransportResponseReceived, Response: ResponseMalformed, TargetKind: snapshot.Target}, nil
	}
}

func searchProviderEvidenceInputs(snapshot LiveAuthorizedStep, result newznab.CommissioningResult, observedAt time.Time, evidenceKey string, verdict ResponseVerdict, providerOutcome string) (LiveAttemptInputs, error) {
	fixture, _, err := resolveSearchFixture(snapshot)
	if err != nil {
		return LiveAttemptInputs{}, err
	}
	proof, err := emptySearchProof(snapshot.Scope, fixture, providerOutcome)
	if err != nil {
		return LiveAttemptInputs{}, err
	}
	return searchEvidenceInputs(snapshot, result, observedAt, evidenceKey, verdict, proof)
}

func emptySearchProof(scope newznab.SearchScope, fixture searchFixture, providerOutcome string) (SearchProof, error) {
	proof := SearchProof{
		SchemaVersion:     DocumentSchemaVersion,
		Scope:             string(scope),
		ProviderOutcome:   providerOutcome,
		IdentityNamespace: fixture.identityNamespace,
		IdentityMatch:     searchIdentityAbsent,
		TitleMatch:        searchTitleNone,
		CompletePackBasis: searchPackNone,
	}
	if scope == newznab.SearchScopeMovie {
		proof.YearMatch = searchYearAbsent
		if fixture.year == nil {
			proof.YearMatch = searchYearNotRequired
		}
		proof.SeasonMatch = searchSeasonNotRequired
	} else {
		proof.YearMatch = searchYearNotRequired
		proof.SeasonMatch = searchSeasonAbsent
		proof.SeasonRequested = fixture.season
	}
	if err := ValidateSearchProof(proof); err != nil {
		return SearchProof{}, err
	}
	return proof, nil
}

func searchEvidenceInputs(snapshot LiveAuthorizedStep, result newznab.CommissioningResult, observedAt time.Time, evidenceKey string, verdict ResponseVerdict, proof SearchProof) (LiveAttemptInputs, error) {
	digest, err := SHA256DigestFromBytes(result.BodySHA256[:])
	if err != nil {
		return LiveAttemptInputs{}, ErrInvalidCommissioningDocument
	}
	raw := proof.RawResultCount
	plausible := proof.PlausibleIdentityCount
	evidence := StepEvidenceDocument{
		SchemaVersion:          DocumentSchemaVersion,
		Key:                    evidenceKey,
		Kind:                   newznab.EvidenceKindControlledProbe,
		Target:                 snapshot.Target,
		ObservedAt:             observedAt,
		SafeOutcome:            searchOutcomeForVerdict(verdict),
		RawResultCount:         raw,
		PlausibleIdentityCount: plausible,
		BodySHA256:             digest,
		Search:                 &proof,
	}
	if err := validateSearchEvidenceShape(evidence); err != nil {
		return LiveAttemptInputs{}, err
	}
	return LiveAttemptInputs{
		Transport:              TransportResponseReceived,
		Response:               verdict,
		TargetKind:             snapshot.Target,
		Evidence:               &evidence,
		RawResultCount:         &raw,
		PlausibleIdentityCount: &plausible,
		RawBodySHA256:          result.BodySHA256[:],
	}, nil
}

// validateSearchEvidenceShape checks the search evidence envelope and the
// closed proof without the generic classifier's all-raw-plausible rule.
func validateSearchEvidenceShape(document StepEvidenceDocument) error {
	if document.SchemaVersion != DocumentSchemaVersion || !evidenceKeyPattern.MatchString(document.Key) || document.Kind != newznab.EvidenceKindControlledProbe || document.ObservedAt.IsZero() || document.BodySHA256.IsZero() || document.CAPS != nil || document.Search == nil || document.Target.Kind != newznab.EvidenceTargetSearchRecipe {
		return ErrInvalidCommissioningDocument
	}
	if err := ValidateSearchProof(*document.Search); err != nil {
		return err
	}
	if document.Search.RawResultCount != document.RawResultCount || document.Search.PlausibleIdentityCount != document.PlausibleIdentityCount {
		return ErrInvalidCommissioningDocument
	}
	return nil
}

// ClassifyLiveAttemptForTarget dispatches to the frozen generic classifier for
// non-search targets and to the search-specific Section-20 classifier for
// search-recipe targets. The generic classifier is unchanged.
func ClassifyLiveAttemptForTarget(inputs LiveAttemptInputs) (ClassifiedCompletion, error) {
	if inputs.TargetKind.Kind == newznab.EvidenceTargetSearchRecipe {
		return ClassifySearchAttempt(inputs)
	}
	return ClassifyLiveAttempt(inputs)
}

// ClassifiedCompletionMatchesInputsForTarget is the target-aware companion of
// ClassifiedCompletionMatchesInputs. Search completions are re-derived with the
// search-specific classifier so a qualifying plausible result among unrelated
// results is accepted without weakening the frozen generic classifier.
func ClassifiedCompletionMatchesInputsForTarget(classified ClassifiedCompletion) bool {
	if classified.Inputs.TargetKind.Kind == newznab.EvidenceTargetSearchRecipe {
		want, err := ClassifySearchAttempt(classified.Inputs)
		if err != nil {
			return false
		}
		return searchCompletionMatches(want, classified)
	}
	return ClassifiedCompletionMatchesInputs(classified)
}

// ClassifySearchAttempt is the operation-local Section-20 classifier. It
// preserves the frozen generic classifier for every search outcome except
// recipe_verified, where Section 20 permits verification when at least one
// independently plausible result meets all required semantic conditions even
// if unrelated results are also present.
func ClassifySearchAttempt(inputs LiveAttemptInputs) (ClassifiedCompletion, error) {
	if inputs.TargetKind.Kind != newznab.EvidenceTargetSearchRecipe {
		return ClassifiedCompletion{}, errInvalidLiveClassification
	}
	if inputs.Transport != TransportResponseReceived || inputs.Response != ResponseRecipeVerified {
		return ClassifyLiveAttempt(inputs)
	}
	result := ClassifiedCompletion{
		Inputs:                 inputs,
		SafeOutcome:            OutcomeRecipeVerified,
		SchedulerResult:        indexers.RecordResult{Outcome: indexers.OutcomeSuccess, Scope: indexers.FailureScopeNone},
		ClearGate:              true,
		Evidence:               inputs.Evidence,
		RawResultCount:         inputs.RawResultCount,
		PlausibleIdentityCount: inputs.PlausibleIdentityCount,
		SafeResponseSHA256:     append([]byte(nil), inputs.SafeResponseSHA256...),
		RawBodySHA256:          append([]byte(nil), inputs.RawBodySHA256...),
	}
	if err := ValidateSearchCompletionEvidence(result); err != nil {
		return ClassifiedCompletion{}, err
	}
	return result, nil
}

// ValidateSearchCompletionEvidence validates a search-recipe completion
// against the closed Section-20 proof and count rules.
func ValidateSearchCompletionEvidence(classified ClassifiedCompletion) error {
	evidence := classified.Evidence
	hasCounts := classified.RawResultCount != nil && classified.PlausibleIdentityCount != nil
	switch classified.SafeOutcome {
	case OutcomeRecipeVerified, OutcomeNoResults, OutcomeNoPlausibleMatch, OutcomeUnsupported, OutcomePermissionRestricted, OutcomeContradicted:
		if evidence == nil || evidence.Search == nil || !hasCounts {
			return ErrInvalidCommissioningDocument
		}
		if evidence.Target.Kind != newznab.EvidenceTargetSearchRecipe || evidence.SafeOutcome != classified.SafeOutcome {
			return ErrInvalidCommissioningDocument
		}
		if err := ValidateSearchProof(*evidence.Search); err != nil {
			return err
		}
		if *classified.RawResultCount != evidence.Search.RawResultCount || *classified.PlausibleIdentityCount != evidence.Search.PlausibleIdentityCount {
			return ErrInvalidCommissioningDocument
		}
		if !bytes.Equal(classified.RawBodySHA256, evidence.BodySHA256.Bytes()) {
			return ErrInvalidCommissioningDocument
		}
		switch classified.SafeOutcome {
		case OutcomeRecipeVerified:
			if evidence.Search.ProviderOutcome != searchProviderNone || len(evidence.Search.ConflictCodes) != 0 || evidence.Search.PlausibleIdentityCount == 0 {
				return ErrInvalidCommissioningDocument
			}
		case OutcomeNoResults:
			if evidence.Search.RawResultCount != 0 {
				return ErrInvalidCommissioningDocument
			}
		case OutcomeNoPlausibleMatch:
			if evidence.Search.RawResultCount == 0 || evidence.Search.PlausibleIdentityCount != 0 || len(evidence.Search.ConflictCodes) != 0 {
				return ErrInvalidCommissioningDocument
			}
		case OutcomeContradicted:
			if len(evidence.Search.ConflictCodes) == 0 {
				return ErrInvalidCommissioningDocument
			}
		case OutcomeUnsupported:
			if evidence.Search.ProviderOutcome != searchProviderUnsupported {
				return ErrInvalidCommissioningDocument
			}
		case OutcomePermissionRestricted:
			if evidence.Search.ProviderOutcome != searchProviderPermissionRestricted {
				return ErrInvalidCommissioningDocument
			}
		}
		return nil
	default:
		if evidence != nil || hasCounts || len(classified.SafeResponseSHA256) != 0 || len(classified.RawBodySHA256) != 0 {
			return ErrInvalidCommissioningDocument
		}
		return nil
	}
}

func searchCompletionMatches(want, got ClassifiedCompletion) bool {
	if want.SafeOutcome != got.SafeOutcome || want.SafeCode != got.SafeCode || want.SchedulerResult != got.SchedulerResult || want.ClearGate != got.ClearGate {
		return false
	}
	if (want.Evidence == nil) != (got.Evidence == nil) {
		return false
	}
	if want.Evidence != nil {
		wantDocument, wantErr := json.Marshal(want.Evidence)
		gotDocument, gotErr := json.Marshal(got.Evidence)
		if wantErr != nil || gotErr != nil || !bytes.Equal(wantDocument, gotDocument) {
			return false
		}
	}
	if !optionalUint16Equal(want.RawResultCount, got.RawResultCount) || !optionalUint16Equal(want.PlausibleIdentityCount, got.PlausibleIdentityCount) {
		return false
	}
	if !bytes.Equal(normalizeDigestBytes(want.SafeResponseSHA256), normalizeDigestBytes(got.SafeResponseSHA256)) || !bytes.Equal(normalizeDigestBytes(want.RawBodySHA256), normalizeDigestBytes(got.RawBodySHA256)) {
		return false
	}
	return true
}

func searchOutcomeForVerdict(verdict ResponseVerdict) SafeOutcome {
	switch verdict {
	case ResponseNoResults:
		return OutcomeNoResults
	case ResponseNoPlausibleMatch:
		return OutcomeNoPlausibleMatch
	case ResponseRecipeVerified:
		return OutcomeRecipeVerified
	case ResponseContradicted:
		return OutcomeContradicted
	case ResponseUnsupported:
		return OutcomeUnsupported
	case ResponsePermissionRestricted:
		return OutcomePermissionRestricted
	default:
		return OutcomeSemanticAmbiguity
	}
}
