package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

// IntegrationConnectionPatch edits a Plex, AltMount or Seerr connection from
// the website. Indexers keep their own patch (their address is part of their
// commissioned capability and cannot change).
type IntegrationConnectionPatch struct {
	Name      *string `json:"name,omitempty"`
	BaseURL   *string `json:"base_url,omitempty"`
	AllowHTTP *bool   `json:"allow_http,omitempty"`
	Enabled   *bool   `json:"enabled,omitempty"`
	Default   *bool   `json:"is_default,omitempty"`
	// AltMount only.
	Category          *string `json:"category,omitempty"`
	LocalReadOnlyRoot *string `json:"local_read_only_root,omitempty"`
	Authentication    *string `json:"authentication,omitempty"`
}

// ValidServiceURL accepts an absolute http(s) URL without credentials, query
// or fragment. Plain http needs allowHTTP.
func ValidServiceURL(raw string, allowHTTP bool) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return parsed.Scheme == "https" || (allowHTTP && parsed.Scheme == "http")
}

func (store *Store) UpdateIntegrationConnection(ctx context.Context, id uuid.UUID, patch IntegrationConnectionPatch, now time.Time) (Integration, error) {
	transaction, err := store.Pool.Begin(ctx)
	if err != nil {
		return Integration{}, err
	}
	defer transaction.Rollback(ctx)
	var kind, name, configText string
	var enabled, isDefault bool
	if err := transaction.QueryRow(ctx, `SELECT kind,name,enabled,is_default,config::text FROM integration_instances WHERE id=$1 FOR UPDATE`, id.String()).Scan(&kind, &name, &enabled, &isDefault, &configText); errors.Is(err, pgx.ErrNoRows) {
		return Integration{}, ErrNotFound
	} else if err != nil {
		return Integration{}, err
	}
	if kind != "plex" && kind != "altmount" && kind != "seerr" {
		return Integration{}, refuse("invalid_integration_kind", "only Plex, AltMount and Seerr connections are edited here", "Change an indexer with PATCH /api/v1/settings/integrations/{id}.")
	}
	config := map[string]any{}
	if err := json.Unmarshal([]byte(configText), &config); err != nil {
		return Integration{}, err
	}
	changed := false
	if patch.Name != nil {
		trimmed := strings.TrimSpace(*patch.Name)
		if trimmed == "" || len(trimmed) > 100 {
			return Integration{}, refuse("invalid_name", "name must be 1-100 characters", "")
		}
		name, changed = trimmed, true
	}
	if patch.AllowHTTP != nil {
		config["allow_http"], changed = *patch.AllowHTTP, true
	}
	if patch.BaseURL != nil {
		config["base_url"], changed = strings.TrimRight(strings.TrimSpace(*patch.BaseURL), "/"), true
	}
	if patch.BaseURL != nil || patch.AllowHTTP != nil {
		base, _ := config["base_url"].(string)
		allow, _ := config["allow_http"].(bool)
		if !ValidServiceURL(base, allow) {
			return Integration{}, refuse("invalid_base_url", "base_url must be an https URL, or http with allow_http, without user, query or fragment", "")
		}
	}
	for key, value := range map[string]*string{"category": patch.Category, "local_read_only_root": patch.LocalReadOnlyRoot, "authentication": patch.Authentication} {
		if value == nil {
			continue
		}
		if kind != "altmount" {
			return Integration{}, refuse("invalid_field", key+" applies only to AltMount", "")
		}
		trimmed := strings.TrimSpace(*value)
		switch {
		case key == "authentication" && trimmed != "anonymous_admin" && trimmed != "session":
			return Integration{}, refuse("invalid_authentication", "authentication must be anonymous_admin or session", "")
		case key == "local_read_only_root" && !strings.HasPrefix(trimmed, "/"):
			return Integration{}, refuse("invalid_local_read_only_root", "local_read_only_root must be an absolute path inside the API container", "")
		case key == "category" && (trimmed == "" || strings.ContainsAny(trimmed, "/\\ ")):
			return Integration{}, refuse("invalid_category", "category must be one word", "")
		}
		config[key], changed = trimmed, true
	}
	if patch.Enabled != nil {
		enabled, changed = *patch.Enabled, true
	}
	if patch.Default != nil {
		isDefault, changed = *patch.Default, true
	}
	if !changed {
		return Integration{}, refuse("empty_patch", "nothing to change", "Send at least one field.")
	}
	if isDefault && !enabled {
		return Integration{}, refuse("invalid_default_change", "a disabled connection cannot be the default", "Enable it first.")
	}
	if isDefault {
		if _, err := transaction.Exec(ctx, `UPDATE integration_instances SET is_default=false WHERE kind=$1 AND id<>$2`, kind, id.String()); err != nil {
			return Integration{}, err
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return Integration{}, err
	}
	if _, err := transaction.Exec(ctx, `UPDATE integration_instances SET name=$2,enabled=$3,is_default=$4,config=$5,updated_at=$6 WHERE id=$1`, id.String(), name, enabled, isDefault, string(encoded), now.UTC()); err != nil {
		return Integration{}, err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO audit_events(id,actor,action,aggregate_type,aggregate_id,detail,occurred_at) VALUES($1,'owner','integration.connection_updated','integration',$2,'{}',$3)`, uuid.NewV7().String(), id.String(), now.UTC()); err != nil {
		return Integration{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return Integration{}, err
	}
	return store.Integration(ctx, id)
}
