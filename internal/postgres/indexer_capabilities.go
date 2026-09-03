package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"uuid"
)

type IndexerCapabilityProfile struct {
	ID                   uuid.UUID       `json:"id"`
	IndexerInstanceID    uuid.UUID       `json:"indexer_instance_id"`
	Revision             uint64          `json:"revision"`
	SchemaVersion        uint16          `json:"schema_version"`
	LifecycleState       string          `json:"lifecycle_state"`
	Document             json.RawMessage `json:"document"`
	DocumentSHA256       []byte          `json:"-"`
	EndpointConfigSHA256 []byte          `json:"-"`
	CreatedAt            time.Time       `json:"created_at"`
	ActivatedAt          *time.Time      `json:"activated_at,omitempty"`
	SupersededAt         *time.Time      `json:"superseded_at,omitempty"`
	InvalidatedAt        *time.Time      `json:"invalidated_at,omitempty"`
	InvalidationReason   string          `json:"invalidation_reason,omitempty"`
}

type IndexerOperationStatus struct {
	IndexerInstanceID   uuid.UUID  `json:"indexer_instance_id"`
	CapabilityProfileID uuid.UUID  `json:"capability_profile_id"`
	OperationKey        string     `json:"operation_key"`
	RecipeKey           string     `json:"recipe_key,omitempty"`
	Availability        string     `json:"availability"`
	LastOutcome         string     `json:"last_outcome,omitempty"`
	SafeCode            string     `json:"safe_code,omitempty"`
	LastFailureScope    string     `json:"last_failure_scope,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	RetryAt             *time.Time `json:"retry_at,omitempty"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt       *time.Time `json:"last_failure_at,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

var (
	ErrInvalidIndexerCapabilityProfile     = errors.New("invalid indexer capability profile")
	ErrIndexerCategoryReviewRequired       = errors.New("indexer category selection requires owner review")
	ErrIndexerEndpointConfigurationChanged = errors.New("indexer endpoint configuration changed after capability observation")
	ErrIndexerProviderContractMismatch     = errors.New("indexer provider contract does not match its capability profile")
	ErrInvalidIndexerOperationStatus       = errors.New("invalid indexer operation status")
	capabilitySafeKeyPattern               = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,99}$`)
)

const capabilityProfileColumns = `id::text,indexer_instance_id::text,revision,schema_version,lifecycle_state,profile_document,document_sha256,endpoint_config_sha256,created_at,activated_at,superseded_at,invalidated_at,COALESCE(invalidation_reason,'')`
const activeCapabilityProfileColumns = `profile.id::text,profile.indexer_instance_id::text,profile.revision,profile.schema_version,profile.lifecycle_state,profile.profile_document,profile.document_sha256,profile.endpoint_config_sha256,profile.created_at,profile.activated_at,profile.superseded_at,profile.invalidated_at,COALESCE(profile.invalidation_reason,'')`

// IndexerEndpointConfigurationDigest returns the non-secret endpoint identity
// that commissioning must bind to any observations it produces.
func (store *Store) IndexerEndpointConfigurationDigest(ctx context.Context, indexerID uuid.UUID) ([]byte, error) {
	if indexerID == (uuid.UUID{}) {
		return nil, ErrInvalidIndexerCapabilityProfile
	}
	var digest []byte
	if err := store.Pool.QueryRow(ctx, `SELECT media_manager_newznab_endpoint_config_sha256(config,secret_ref) FROM integration_instances WHERE id=$1 AND kind='newznab'`, indexerID.String()).Scan(&digest); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if len(digest) != 32 {
		return nil, ErrInvalidIndexerCapabilityProfile
	}
	return append([]byte(nil), digest...), nil
}

// CreateIndexerCapabilityProfile always appends a new immutable draft revision.
// The integration row lock serializes revision allocation without requiring
// callers to retry a serializable transaction. expectedEndpointDigest must be
// captured before commissioning so evidence cannot be rebound to later config.
func (store *Store) CreateIndexerCapabilityProfile(ctx context.Context, indexerID uuid.UUID, document json.RawMessage, expectedEndpointDigest []byte, now time.Time) (IndexerCapabilityProfile, error) {
	profile, canonical, err := canonicalCapabilityProfile(document)
	if err != nil || indexerID == (uuid.UUID{}) || len(expectedEndpointDigest) != 32 {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	now = capabilityTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return IndexerCapabilityProfile{}, err
	}
	defer transaction.Rollback(ctx)
	var kind string
	var endpointDigest []byte
	if err := transaction.QueryRow(ctx, `SELECT kind,media_manager_newznab_endpoint_config_sha256(config,secret_ref) FROM integration_instances WHERE id=$1 FOR UPDATE`, indexerID.String()).Scan(&kind, &endpointDigest); errors.Is(err, pgx.ErrNoRows) {
		return IndexerCapabilityProfile{}, ErrNotFound
	} else if err != nil {
		return IndexerCapabilityProfile{}, err
	}
	if kind != "newznab" {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	if len(endpointDigest) != 32 {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	if !bytes.Equal(endpointDigest, expectedEndpointDigest) {
		return IndexerCapabilityProfile{}, ErrIndexerEndpointConfigurationChanged
	}
	var revision int64
	if err := transaction.QueryRow(ctx, `SELECT COALESCE(max(revision),0)+1 FROM indexer_capability_profiles WHERE indexer_instance_id=$1`, indexerID.String()).Scan(&revision); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	id := uuid.NewV7()
	if _, err := transaction.Exec(ctx, `INSERT INTO indexer_capability_profiles(id,indexer_instance_id,revision,schema_version,lifecycle_state,profile_document,document_sha256,endpoint_config_sha256,created_at) VALUES($1,$2,$3,$4,'draft',$5,sha256(convert_to($5::jsonb::text,'UTF8')),$6,$7)`, id.String(), indexerID.String(), revision, profile.SchemaVersion, string(canonical), endpointDigest, now); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	detail, _ := json.Marshal(map[string]any{"profile_id": id.String(), "revision": revision, "schema_version": profile.SchemaVersion})
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'system','newznab.capability_profile_created','integration',$2,$3,$4)`, uuid.NewV7().String(), indexerID.String(), string(detail), now); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	return store.IndexerCapabilityProfile(ctx, id)
}

// ActivateIndexerCapabilityProfile is the explicit owner review gate. It never
// edits category selection; that selection must already have been reviewed.
func (store *Store) ActivateIndexerCapabilityProfile(ctx context.Context, indexerID, profileID uuid.UUID, actor string, now time.Time) (IndexerCapabilityProfile, error) {
	if indexerID == (uuid.UUID{}) || profileID == (uuid.UUID{}) || strings.TrimSpace(actor) != "owner" {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	now = capabilityTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return IndexerCapabilityProfile{}, err
	}
	defer transaction.Rollback(ctx)
	var configuration []byte
	var currentEndpointDigest []byte
	if err := transaction.QueryRow(ctx, `SELECT config,media_manager_newznab_endpoint_config_sha256(config,secret_ref) FROM integration_instances WHERE id=$1 AND kind='newznab' FOR UPDATE`, indexerID.String()).Scan(&configuration, &currentEndpointDigest); errors.Is(err, pgx.ErrNoRows) {
		return IndexerCapabilityProfile{}, ErrNotFound
	} else if err != nil {
		return IndexerCapabilityProfile{}, err
	}
	if len(currentEndpointDigest) != 32 {
		return IndexerCapabilityProfile{}, ErrIndexerEndpointConfigurationChanged
	}
	var state string
	var document, documentDigest, observedEndpointDigest []byte
	var documentDigestMatches bool
	if err := transaction.QueryRow(ctx, `SELECT lifecycle_state,profile_document,document_sha256,endpoint_config_sha256,document_sha256=sha256(convert_to(profile_document::text,'UTF8')) FROM indexer_capability_profiles WHERE indexer_instance_id=$1 AND id=$2 FOR UPDATE`, indexerID.String(), profileID.String()).Scan(&state, &document, &documentDigest, &observedEndpointDigest, &documentDigestMatches); errors.Is(err, pgx.ErrNoRows) {
		return IndexerCapabilityProfile{}, ErrNotFound
	} else if err != nil {
		return IndexerCapabilityProfile{}, err
	}
	if state != "draft" && state != "active" {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	profile, err := validateStoredCapabilityProfile(document, documentDigest)
	if !documentDigestMatches || err != nil || newznab.ValidateCapabilityProfileForActivation(profile) != nil {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	configurationState, err := capabilityConfigurationState(configuration)
	if err != nil {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	if configurationState.CategorySelectionReviewRequired {
		return IndexerCapabilityProfile{}, ErrIndexerCategoryReviewRequired
	}
	if !providerContractAligned(configurationState, profile.ProviderContract) {
		return IndexerCapabilityProfile{}, ErrIndexerProviderContractMismatch
	}
	if !bytes.Equal(currentEndpointDigest, observedEndpointDigest) {
		return IndexerCapabilityProfile{}, ErrIndexerEndpointConfigurationChanged
	}
	if state == "active" {
		if err := transaction.Commit(ctx); err != nil {
			return IndexerCapabilityProfile{}, err
		}
		return store.IndexerCapabilityProfile(ctx, profileID)
	}
	if _, err := transaction.Exec(ctx, `UPDATE indexer_capability_profiles SET lifecycle_state='superseded',superseded_at=$2 WHERE indexer_instance_id=$1 AND lifecycle_state='active'`, indexerID.String(), now); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	command, err := transaction.Exec(ctx, `UPDATE indexer_capability_profiles SET lifecycle_state='active',activated_at=$3 WHERE indexer_instance_id=$1 AND id=$2 AND lifecycle_state='draft'`, indexerID.String(), profileID.String(), now)
	if err != nil {
		return IndexerCapabilityProfile{}, err
	}
	if command.RowsAffected() != 1 {
		return IndexerCapabilityProfile{}, ErrConcurrentModification
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','newznab.capability_profile_activated','integration',$2,$3,$4)`, uuid.NewV7().String(), indexerID.String(), `{"profile_id":"`+profileID.String()+`"}`, now); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	return store.IndexerCapabilityProfile(ctx, profileID)
}

func (store *Store) InvalidateIndexerCapabilityProfile(ctx context.Context, indexerID, profileID uuid.UUID, reason, actor string, now time.Time) (IndexerCapabilityProfile, error) {
	reason, actor = strings.TrimSpace(reason), strings.TrimSpace(actor)
	if indexerID == (uuid.UUID{}) || profileID == (uuid.UUID{}) || actor != "owner" || !capabilitySafeKeyPattern.MatchString(reason) {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	now = capabilityTime(now)
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return IndexerCapabilityProfile{}, err
	}
	defer transaction.Rollback(ctx)
	var state string
	var document, documentDigest []byte
	var digestMatches bool
	if err := transaction.QueryRow(ctx, `SELECT lifecycle_state,profile_document,document_sha256,document_sha256=sha256(convert_to(profile_document::text,'UTF8')) FROM indexer_capability_profiles WHERE indexer_instance_id=$1 AND id=$2 FOR UPDATE`, indexerID.String(), profileID.String()).Scan(&state, &document, &documentDigest, &digestMatches); errors.Is(err, pgx.ErrNoRows) {
		return IndexerCapabilityProfile{}, ErrNotFound
	} else if err != nil {
		return IndexerCapabilityProfile{}, err
	}
	if !digestMatches {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	if _, err := validateStoredCapabilityProfile(document, documentDigest); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	if state == "invalidated" {
		if err := transaction.Commit(ctx); err != nil {
			return IndexerCapabilityProfile{}, err
		}
		return store.IndexerCapabilityProfile(ctx, profileID)
	}
	if state != "draft" && state != "active" {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	if _, err := transaction.Exec(ctx, `UPDATE indexer_capability_profiles SET lifecycle_state='invalidated',invalidated_at=$3,invalidation_reason=$4 WHERE indexer_instance_id=$1 AND id=$2`, indexerID.String(), profileID.String(), now, reason); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','newznab.capability_profile_invalidated','integration',$2,$3,$4)`, uuid.NewV7().String(), indexerID.String(), `{"profile_id":"`+profileID.String()+`","reason":"`+reason+`"}`, now); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	return store.IndexerCapabilityProfile(ctx, profileID)
}

func (store *Store) IndexerCapabilityProfile(ctx context.Context, profileID uuid.UUID) (IndexerCapabilityProfile, error) {
	result, err := scanIndexerCapabilityProfile(store.Pool.QueryRow(ctx, `SELECT `+capabilityProfileColumns+`,document_sha256=sha256(convert_to(profile_document::text,'UTF8')) FROM indexer_capability_profiles WHERE id=$1`, profileID.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return IndexerCapabilityProfile{}, ErrNotFound
	}
	return result, err
}

func (store *Store) ActiveIndexerCapabilityProfile(ctx context.Context, indexerID uuid.UUID) (IndexerCapabilityProfile, error) {
	result, configuration, endpointDigestMatches, err := scanActiveIndexerCapabilityProfile(store.Pool.QueryRow(ctx, `SELECT `+activeCapabilityProfileColumns+`,profile.document_sha256=sha256(convert_to(profile.profile_document::text,'UTF8')),integration.config,profile.endpoint_config_sha256 IS NOT DISTINCT FROM media_manager_newznab_endpoint_config_sha256(integration.config,integration.secret_ref)
		FROM indexer_capability_profiles profile
		JOIN integration_instances integration ON integration.id=profile.indexer_instance_id AND integration.kind='newznab'
		WHERE profile.indexer_instance_id=$1 AND profile.lifecycle_state='active'`, indexerID.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return IndexerCapabilityProfile{}, ErrNotFound
	}
	if err != nil {
		return IndexerCapabilityProfile{}, err
	}
	configurationState, err := capabilityConfigurationState(configuration)
	if err != nil {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	if configurationState.CategorySelectionReviewRequired {
		return IndexerCapabilityProfile{}, ErrIndexerCategoryReviewRequired
	}
	profile, err := newznab.DecodeCapabilityProfile(result.Document)
	if err != nil {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	if !providerContractAligned(configurationState, profile.ProviderContract) {
		return IndexerCapabilityProfile{}, ErrIndexerProviderContractMismatch
	}
	if !endpointDigestMatches {
		return IndexerCapabilityProfile{}, ErrIndexerEndpointConfigurationChanged
	}
	return result, nil
}

func (store *Store) ListIndexerCapabilityProfiles(ctx context.Context, indexerID uuid.UUID) ([]IndexerCapabilityProfile, error) {
	rows, err := store.Pool.Query(ctx, `SELECT `+capabilityProfileColumns+`,document_sha256=sha256(convert_to(profile_document::text,'UTF8')) FROM indexer_capability_profiles WHERE indexer_instance_id=$1 ORDER BY revision`, indexerID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []IndexerCapabilityProfile{}
	for rows.Next() {
		item, err := scanIndexerCapabilityProfile(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) SaveIndexerOperationStatus(ctx context.Context, status IndexerOperationStatus) error {
	status.OperationKey, status.RecipeKey = strings.TrimSpace(status.OperationKey), strings.TrimSpace(status.RecipeKey)
	status.LastOutcome, status.SafeCode, status.LastFailureScope = strings.TrimSpace(status.LastOutcome), strings.TrimSpace(status.SafeCode), strings.TrimSpace(status.LastFailureScope)
	if !validIndexerOperationStatus(status) {
		return ErrInvalidIndexerOperationStatus
	}
	if err := store.validateIndexerOperationStatusTarget(ctx, status.IndexerInstanceID, status.CapabilityProfileID, status.OperationKey, status.RecipeKey); err != nil {
		return err
	}
	_, err := store.Pool.Exec(ctx, `INSERT INTO indexer_operation_status(indexer_instance_id,capability_profile_id,operation_key,recipe_key,availability,last_outcome,safe_code,last_failure_scope,consecutive_failures,retry_at,last_success_at,last_failure_at,updated_at)
		VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),$9,$10,$11,$12,$13)
		ON CONFLICT(capability_profile_id,operation_key,recipe_key) DO UPDATE SET indexer_instance_id=EXCLUDED.indexer_instance_id,availability=EXCLUDED.availability,last_outcome=EXCLUDED.last_outcome,safe_code=EXCLUDED.safe_code,last_failure_scope=EXCLUDED.last_failure_scope,consecutive_failures=EXCLUDED.consecutive_failures,retry_at=EXCLUDED.retry_at,last_success_at=EXCLUDED.last_success_at,last_failure_at=EXCLUDED.last_failure_at,updated_at=EXCLUDED.updated_at
		WHERE indexer_operation_status.updated_at < EXCLUDED.updated_at`, status.IndexerInstanceID.String(), status.CapabilityProfileID.String(), status.OperationKey, status.RecipeKey, status.Availability, status.LastOutcome, status.SafeCode, status.LastFailureScope, status.ConsecutiveFailures, status.RetryAt, status.LastSuccessAt, status.LastFailureAt, status.UpdatedAt.UTC())
	return err
}

func (store *Store) LoadIndexerOperationStatus(ctx context.Context, indexerID, profileID uuid.UUID, operationKey, recipeKey string) (IndexerOperationStatus, error) {
	result := IndexerOperationStatus{IndexerInstanceID: indexerID, CapabilityProfileID: profileID, OperationKey: strings.TrimSpace(operationKey), RecipeKey: strings.TrimSpace(recipeKey), Availability: "unknown"}
	if err := store.validateIndexerOperationStatusTarget(ctx, indexerID, profileID, result.OperationKey, result.RecipeKey); err != nil {
		return IndexerOperationStatus{}, err
	}
	err := store.Pool.QueryRow(ctx, `SELECT availability,COALESCE(last_outcome,''),COALESCE(safe_code,''),COALESCE(last_failure_scope,''),consecutive_failures,retry_at,last_success_at,last_failure_at,updated_at FROM indexer_operation_status WHERE indexer_instance_id=$1 AND capability_profile_id=$2 AND operation_key=$3 AND recipe_key=$4`, indexerID.String(), profileID.String(), result.OperationKey, result.RecipeKey).Scan(&result.Availability, &result.LastOutcome, &result.SafeCode, &result.LastFailureScope, &result.ConsecutiveFailures, &result.RetryAt, &result.LastSuccessAt, &result.LastFailureAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	return result, err
}

func (store *Store) validateIndexerOperationStatusTarget(ctx context.Context, indexerID, profileID uuid.UUID, operationKey, recipeKey string) error {
	if indexerID == (uuid.UUID{}) || profileID == (uuid.UUID{}) || !newznab.ValidOperation(newznab.Operation(operationKey)) || recipeKey != "" && (operationKey != string(newznab.OperationSearch) || !capabilitySafeKeyPattern.MatchString(recipeKey)) {
		return ErrInvalidIndexerOperationStatus
	}
	profile, err := store.IndexerCapabilityProfile(ctx, profileID)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidIndexerCapabilityProfile) {
			return ErrInvalidIndexerOperationStatus
		}
		return err
	}
	if profile.IndexerInstanceID != indexerID || profile.ActivatedAt == nil {
		return ErrInvalidIndexerOperationStatus
	}
	if recipeKey == "" {
		return nil
	}
	document, err := newznab.DecodeCapabilityProfile(profile.Document)
	if err != nil {
		return ErrInvalidIndexerOperationStatus
	}
	for _, recipe := range document.SearchRecipes {
		if recipe.Key == recipeKey {
			return nil
		}
	}
	return ErrInvalidIndexerOperationStatus
}

type capabilityProfileScanner interface {
	Scan(...any) error
}

func scanIndexerCapabilityProfile(row capabilityProfileScanner) (IndexerCapabilityProfile, error) {
	return scanIndexerCapabilityProfileFields(row)
}

func scanIndexerCapabilityProfileFields(row capabilityProfileScanner, extraDestinations ...any) (IndexerCapabilityProfile, error) {
	var result IndexerCapabilityProfile
	var idText, indexerText string
	var revision int64
	var schemaVersion int16
	var document []byte
	var digestMatches bool
	destinations := []any{&idText, &indexerText, &revision, &schemaVersion, &result.LifecycleState, &document, &result.DocumentSHA256, &result.EndpointConfigSHA256, &result.CreatedAt, &result.ActivatedAt, &result.SupersededAt, &result.InvalidatedAt, &result.InvalidationReason, &digestMatches}
	destinations = append(destinations, extraDestinations...)
	if err := row.Scan(destinations...); err != nil {
		return IndexerCapabilityProfile{}, err
	}
	if !digestMatches {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	decoded, err := newznab.DecodeCapabilityProfile(document)
	if err != nil || decoded.SchemaVersion != uint16(schemaVersion) {
		return IndexerCapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	result.ID, _ = uuid.Parse(idText)
	result.IndexerInstanceID, _ = uuid.Parse(indexerText)
	result.Revision = uint64(revision)
	result.SchemaVersion = uint16(schemaVersion)
	result.Document = append(json.RawMessage(nil), document...)
	result.DocumentSHA256 = append([]byte(nil), result.DocumentSHA256...)
	result.EndpointConfigSHA256 = append([]byte(nil), result.EndpointConfigSHA256...)
	return result, nil
}

func scanActiveIndexerCapabilityProfile(row capabilityProfileScanner) (IndexerCapabilityProfile, []byte, bool, error) {
	var configuration []byte
	var endpointDigestMatches bool
	profile, err := scanIndexerCapabilityProfileFields(row, &configuration, &endpointDigestMatches)
	return profile, configuration, endpointDigestMatches, err
}

func canonicalCapabilityProfile(document json.RawMessage) (newznab.CapabilityProfile, []byte, error) {
	decoded, err := newznab.DecodeCapabilityProfile(document)
	if err != nil {
		return newznab.CapabilityProfile{}, nil, ErrInvalidIndexerCapabilityProfile
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return newznab.CapabilityProfile{}, nil, ErrInvalidIndexerCapabilityProfile
	}
	return decoded, canonical, nil
}

func validateStoredCapabilityProfile(document, expectedDigest []byte) (newznab.CapabilityProfile, error) {
	decoded, err := newznab.DecodeCapabilityProfile(document)
	if err != nil || len(expectedDigest) != 32 {
		return newznab.CapabilityProfile{}, ErrInvalidIndexerCapabilityProfile
	}
	return decoded, nil
}

type capabilityConfigState struct {
	CategorySelectionReviewRequired bool
	ProviderContractKey             string
	BaseURL                         string
}

func capabilityConfigurationState(configuration []byte) (capabilityConfigState, error) {
	var values struct {
		CategorySelectionReviewRequired *bool  `json:"category_selection_review_required"`
		ProviderContractKey             string `json:"provider_contract_key"`
		BaseURL                         string `json:"base_url"`
	}
	if json.Unmarshal(configuration, &values) != nil || values.CategorySelectionReviewRequired == nil {
		return capabilityConfigState{}, ErrInvalidIndexerCapabilityProfile
	}
	return capabilityConfigState{CategorySelectionReviewRequired: *values.CategorySelectionReviewRequired, ProviderContractKey: strings.TrimSpace(values.ProviderContractKey), BaseURL: strings.TrimSpace(values.BaseURL)}, nil
}

func providerContractAligned(configuration capabilityConfigState, observed *newznab.ProviderContractRef) bool {
	configured := strings.TrimSpace(configuration.ProviderContractKey)
	if observed == nil {
		return configured == ""
	}
	return configured != "" && configured == observed.Key && newznab.ProviderContractAcceptsEndpoint(*observed, configuration.BaseURL)
}

func validIndexerOperationStatus(status IndexerOperationStatus) bool {
	if status.IndexerInstanceID == (uuid.UUID{}) || status.CapabilityProfileID == (uuid.UUID{}) || !newznab.ValidOperation(newznab.Operation(status.OperationKey)) || status.RecipeKey != "" && (status.OperationKey != string(newznab.OperationSearch) || !capabilitySafeKeyPattern.MatchString(status.RecipeKey)) || status.ConsecutiveFailures < 0 || status.UpdatedAt.IsZero() {
		return false
	}
	switch status.Availability {
	case "unknown", "ready", "deferred", "degraded":
	default:
		return false
	}
	for _, value := range []string{status.LastOutcome, status.SafeCode} {
		if value != "" && !capabilitySafeKeyPattern.MatchString(strings.TrimSpace(value)) {
			return false
		}
	}
	switch status.LastFailureScope {
	case "", "instance", "operation":
	default:
		return false
	}
	return true
}

func capabilityTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}
