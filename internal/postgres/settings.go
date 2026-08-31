package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/releases"
	"github.com/media-manager/media-manager/internal/subtitles"
)

type Integration struct {
	ID        uuid.UUID       `json:"id"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Enabled   bool            `json:"enabled"`
	Default   bool            `json:"is_default"`
	Config    json.RawMessage `json:"config"`
	SecretRef string          `json:"secret_ref,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}
type LibraryRoot struct {
	ID                uuid.UUID         `json:"id"`
	MediaKind         catalog.MediaKind `json:"media_kind"`
	Name              string            `json:"name"`
	DestinationRoot   string            `json:"destination_root"`
	PlexVisibleRoot   string            `json:"plex_visible_root"`
	SourceVisibleRoot string            `json:"source_visible_root"`
	PlexInstanceID    *uuid.UUID        `json:"plex_instance_id,omitempty"`
	PlexLibraryID     string            `json:"plex_library_id,omitempty"`
	Default           bool              `json:"is_default"`
	Enabled           bool              `json:"enabled"`
}
type QualityProfile struct {
	ID              uuid.UUID         `json:"id"`
	MediaKind       catalog.MediaKind `json:"media_kind"`
	Name            string            `json:"name"`
	PolicyVersion   uint64            `json:"policy_version"`
	Policy          json.RawMessage   `json:"policy"`
	UpgradesEnabled bool              `json:"upgrades_enabled"`
	Default         bool              `json:"is_default"`
	Enabled         bool              `json:"enabled"`
}

var ErrDuplicateIndexerURL = errors.New("Newznab endpoint already exists")

func (store *Store) CreateIntegration(ctx context.Context, integration Integration) (Integration, error) {
	if integration.ID == (uuid.UUID{}) {
		integration.ID = uuid.NewV7()
	}
	integration.Kind, integration.Name, integration.SecretRef = strings.ToLower(strings.TrimSpace(integration.Kind)), strings.TrimSpace(integration.Name), strings.TrimSpace(integration.SecretRef)
	if integration.Name == "" || !allowedIntegration(integration.Kind) || !json.Valid(integration.Config) {
		return Integration{}, errors.New("invalid integration")
	}
	var newznabURL string
	if integration.Kind == "newznab" {
		canonical, endpoint, err := canonicalNewznabConfig(integration.Config, false)
		if err != nil {
			return Integration{}, err
		}
		integration.Config, newznabURL = canonical, endpoint
		integration.Enabled, integration.Default = false, false
	}
	now := time.Now().UTC()
	integration.CreatedAt, integration.UpdatedAt = now, now
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return Integration{}, err
	}
	defer transaction.Rollback(ctx)
	if integration.Kind == "newznab" {
		if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "newznab:"+newznabURL); err != nil {
			return Integration{}, err
		}
		var exists bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_instances WHERE kind='newznab' AND config->>'base_url'=$1)`, newznabURL).Scan(&exists); err != nil {
			return Integration{}, err
		}
		if exists {
			return Integration{}, fmt.Errorf("%w: %s", ErrDuplicateIndexerURL, newznabURL)
		}
	}
	if integration.Default {
		if _, err := transaction.Exec(ctx, `UPDATE integration_instances SET is_default=false WHERE kind=$1`, integration.Kind); err != nil {
			return Integration{}, err
		}
	}
	_, err = transaction.Exec(ctx, `INSERT INTO integration_instances(id,kind,name,enabled,is_default,config,secret_ref,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$8)`, integration.ID.String(), integration.Kind, integration.Name, integration.Enabled, integration.Default, string(integration.Config), integration.SecretRef, now)
	if err != nil {
		return Integration{}, err
	}
	return integration, transaction.Commit(ctx)
}

func canonicalNewznabConfig(config json.RawMessage, preserveVerified bool) (json.RawMessage, string, error) {
	var values map[string]any
	if err := json.Unmarshal(config, &values); err != nil {
		return nil, "", errors.New("invalid Newznab configuration")
	}
	endpoint, _ := values["base_url"].(string)
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	allowHTTP, _ := values["allow_http"].(bool)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http")) {
		return nil, "", errors.New("invalid Newznab endpoint")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	canonical := parsed.String()
	values["base_url"] = canonical
	if !preserveVerified {
		values["mode_verified"] = false
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, "", err
	}
	return encoded, canonical, nil
}

func (store *Store) ListIntegrations(ctx context.Context) ([]Integration, error) {
	rows, err := store.Pool.Query(ctx, `SELECT id::text,kind,name,enabled,is_default,config,COALESCE(secret_ref,''),created_at,updated_at FROM integration_instances ORDER BY kind,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Integration{}
	for rows.Next() {
		var item Integration
		var idText string
		var config []byte
		if err := rows.Scan(&idText, &item.Kind, &item.Name, &item.Enabled, &item.Default, &config, &item.SecretRef, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.ID, _ = uuid.Parse(idText)
		item.Config = append(json.RawMessage(nil), config...)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) CreateLibraryRoot(ctx context.Context, root LibraryRoot) (LibraryRoot, error) {
	if root.ID == (uuid.UUID{}) {
		root.ID = uuid.NewV7()
	}
	plexLibraryID, plexLibraryErr := strconv.ParseInt(strings.TrimSpace(root.PlexLibraryID), 10, 64)
	if root.MediaKind != catalog.KindMovie && root.MediaKind != catalog.KindSeries || strings.TrimSpace(root.Name) == "" || !filepath.IsAbs(root.DestinationRoot) || !filepath.IsAbs(root.PlexVisibleRoot) || !filepath.IsAbs(root.SourceVisibleRoot) || root.PlexInstanceID == nil || plexLibraryErr != nil || plexLibraryID <= 0 {
		return LibraryRoot{}, errors.New("invalid library root")
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return LibraryRoot{}, err
	}
	defer transaction.Rollback(ctx)
	var plexReady bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM integration_instances WHERE id=$1 AND kind='plex' AND enabled)`, root.PlexInstanceID.String()).Scan(&plexReady); err != nil {
		return LibraryRoot{}, err
	}
	if !plexReady {
		return LibraryRoot{}, errors.New("library root requires an enabled Plex integration")
	}
	if root.Default {
		if _, err := transaction.Exec(ctx, `UPDATE library_roots SET is_default=false WHERE media_kind=$1`, root.MediaKind); err != nil {
			return LibraryRoot{}, err
		}
	}
	plexInstance := root.PlexInstanceID.String()
	root.DestinationRoot = filepath.Clean(root.DestinationRoot)
	root.PlexVisibleRoot = filepath.Clean(root.PlexVisibleRoot)
	root.SourceVisibleRoot = filepath.Clean(root.SourceVisibleRoot)
	root.PlexLibraryID = strings.TrimSpace(root.PlexLibraryID)
	if _, err := transaction.Exec(ctx, `INSERT INTO library_roots(id,media_kind,name,destination_root,plex_visible_root,source_visible_root,plex_instance_id,plex_library_id,is_default,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,$10)`, root.ID.String(), root.MediaKind, strings.TrimSpace(root.Name), filepath.Clean(root.DestinationRoot), filepath.Clean(root.PlexVisibleRoot), filepath.Clean(root.SourceVisibleRoot), plexInstance, root.PlexLibraryID, root.Default, root.Enabled); err != nil {
		return LibraryRoot{}, err
	}
	return root, transaction.Commit(ctx)
}

func (store *Store) ListLibraryRoots(ctx context.Context) ([]LibraryRoot, error) {
	rows, err := store.Pool.Query(ctx, `SELECT id::text,media_kind,name,destination_root,plex_visible_root,source_visible_root,plex_instance_id::text,COALESCE(plex_library_id,''),is_default,enabled FROM library_roots ORDER BY media_kind,is_default DESC,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []LibraryRoot{}
	for rows.Next() {
		var root LibraryRoot
		var idText string
		var plexText *string
		if err := rows.Scan(&idText, &root.MediaKind, &root.Name, &root.DestinationRoot, &root.PlexVisibleRoot, &root.SourceVisibleRoot, &plexText, &root.PlexLibraryID, &root.Default, &root.Enabled); err != nil {
			return nil, err
		}
		root.ID, _ = uuid.Parse(idText)
		if plexText != nil {
			parsed, parseErr := uuid.Parse(*plexText)
			if parseErr != nil {
				return nil, parseErr
			}
			root.PlexInstanceID = &parsed
		}
		result = append(result, root)
	}
	return result, rows.Err()
}

func (store *Store) CreateQualityProfile(ctx context.Context, profile QualityProfile) (QualityProfile, error) {
	if profile.ID == (uuid.UUID{}) {
		profile.ID = uuid.NewV7()
	}
	profile, err := canonicalQualityProfile(profile)
	if err != nil {
		return QualityProfile{}, err
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return QualityProfile{}, err
	}
	defer transaction.Rollback(ctx)
	if profile.Default {
		if _, err := transaction.Exec(ctx, `UPDATE quality_profiles SET is_default=false WHERE media_kind=$1`, profile.MediaKind); err != nil {
			return QualityProfile{}, err
		}
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO quality_profiles(id,media_kind,name,policy_version,policy,upgrades_enabled,is_default,enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, profile.ID.String(), profile.MediaKind, profile.Name, int64(profile.PolicyVersion), string(profile.Policy), profile.UpgradesEnabled, profile.Default, profile.Enabled); err != nil {
		return QualityProfile{}, err
	}
	return profile, transaction.Commit(ctx)
}

func canonicalQualityProfile(profile QualityProfile) (QualityProfile, error) {
	if profile.MediaKind != catalog.KindMovie && profile.MediaKind != catalog.KindSeries || strings.TrimSpace(profile.Name) == "" || !json.Valid(profile.Policy) {
		return QualityProfile{}, errors.New("invalid quality profile")
	}
	canonical, _, err := subtitles.CanonicalizePolicy(profile.Policy)
	if err != nil {
		return QualityProfile{}, err
	}
	profile.Policy = canonical
	var wrapped struct {
		Rules []releases.Rule `json:"rules"`
	}
	var rules []releases.Rule
	if err := json.Unmarshal(profile.Policy, &wrapped); err == nil && wrapped.Rules != nil {
		rules = wrapped.Rules
	} else if err := json.Unmarshal(profile.Policy, &rules); err != nil {
		return QualityProfile{}, errors.New("quality profile policy must contain release rules")
	}
	if err := releases.ValidateRules(rules); err != nil {
		return QualityProfile{}, errors.New("quality profile contains invalid release rules")
	}
	if profile.PolicyVersion == 0 {
		profile.PolicyVersion = 1
	}
	profile.Name = strings.TrimSpace(profile.Name)
	return profile, nil
}

func (store *Store) UpdateQualityProfile(ctx context.Context, profile QualityProfile) (QualityProfile, error) {
	if profile.ID == (uuid.UUID{}) {
		return QualityProfile{}, errors.New("quality profile ID is required")
	}
	profile, err := canonicalQualityProfile(profile)
	if err != nil {
		return QualityProfile{}, err
	}
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return QualityProfile{}, err
	}
	defer transaction.Rollback(ctx)
	var currentKind catalog.MediaKind
	var currentVersion int64
	if err := transaction.QueryRow(ctx, `SELECT media_kind,policy_version FROM quality_profiles WHERE id=$1 FOR UPDATE`, profile.ID.String()).Scan(&currentKind, &currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return QualityProfile{}, ErrNotFound
	} else if err != nil {
		return QualityProfile{}, err
	}
	if currentKind != profile.MediaKind {
		return QualityProfile{}, errors.New("quality profile media kind cannot change")
	}
	profile.PolicyVersion = uint64(currentVersion + 1)
	if profile.Default {
		if _, err := transaction.Exec(ctx, `UPDATE quality_profiles SET is_default=false WHERE media_kind=$1 AND id<>$2`, profile.MediaKind, profile.ID.String()); err != nil {
			return QualityProfile{}, err
		}
	}
	command, err := transaction.Exec(ctx, `UPDATE quality_profiles SET name=$2,policy_version=$3,policy=$4,upgrades_enabled=$5,is_default=$6,enabled=$7 WHERE id=$1`, profile.ID.String(), profile.Name, int64(profile.PolicyVersion), string(profile.Policy), profile.UpgradesEnabled, profile.Default, profile.Enabled)
	if err != nil {
		return QualityProfile{}, err
	}
	if command.RowsAffected() != 1 {
		return QualityProfile{}, ErrConcurrentModification
	}
	return profile, transaction.Commit(ctx)
}

func (store *Store) ListQualityProfiles(ctx context.Context) ([]QualityProfile, error) {
	rows, err := store.Pool.Query(ctx, `SELECT id::text,media_kind,name,policy_version,policy,upgrades_enabled,is_default,enabled FROM quality_profiles ORDER BY media_kind,is_default DESC,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []QualityProfile{}
	for rows.Next() {
		var profile QualityProfile
		var idText string
		var version int64
		var policy []byte
		if err := rows.Scan(&idText, &profile.MediaKind, &profile.Name, &version, &policy, &profile.UpgradesEnabled, &profile.Default, &profile.Enabled); err != nil {
			return nil, err
		}
		profile.ID, _ = uuid.Parse(idText)
		profile.PolicyVersion = uint64(version)
		profile.Policy = append(json.RawMessage(nil), policy...)
		result = append(result, profile)
	}
	return result, rows.Err()
}

func (store *Store) Integration(ctx context.Context, id uuid.UUID) (Integration, error) {
	var item Integration
	var idText string
	var config []byte
	err := store.Pool.QueryRow(ctx, `SELECT id::text,kind,name,enabled,is_default,config,COALESCE(secret_ref,''),created_at,updated_at FROM integration_instances WHERE id=$1`, id.String()).Scan(&idText, &item.Kind, &item.Name, &item.Enabled, &item.Default, &config, &item.SecretRef, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	}
	if err != nil {
		return Integration{}, err
	}
	item.ID, _ = uuid.Parse(idText)
	item.Config = append(json.RawMessage(nil), config...)
	return item, nil
}

func allowedIntegration(kind string) bool {
	switch kind {
	case "newznab", "altmount", "plex", "seerr":
		return true
	default:
		return false
	}
}
