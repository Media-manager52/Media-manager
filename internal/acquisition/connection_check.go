package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/plex"
	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/postgres"
)

// CheckAltMount logs in to an AltMount integration exactly as acquisition
// does and confirms its local read-only root is visible inside the API
// container. It submits nothing.
func CheckAltMount(ctx context.Context, integration postgres.Integration, resolveSecret SecretResolver, transport http.RoundTripper) error {
	var config altMountConfig
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return errors.New("AltMount configuration is not valid JSON")
	}
	client, err := newAltMountClient(config, integration.SecretRef, resolveSecret, transport)
	if err != nil {
		return fmt.Errorf("AltMount settings are incomplete: %w", err)
	}
	if _, err := client.Login(ctx); err != nil {
		return fmt.Errorf("AltMount did not accept the login: %w", err)
	}
	_ = client.Logout(ctx)
	root := strings.TrimSpace(config.LocalReadOnlyRoot)
	if root == "" {
		return errors.New("AltMount answered, but local_read_only_root is empty")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("AltMount answered, but %s is not a folder Media Manager can see", root)
	}
	return nil
}

// PlexLibrary is one Plex library as the website shows it when creating a
// library root.
type PlexLibrary struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Type      string   `json:"type"`
	Locations []string `json:"locations"`
}

type serviceConnectionConfig struct {
	BaseURL   string `json:"base_url"`
	AllowHTTP bool   `json:"allow_http"`
}

// PlexLibraries lists a Plex connection's libraries with its saved token. It
// is also the Plex connection check: it changes nothing.
func PlexLibraries(ctx context.Context, integration postgres.Integration, resolveSecret SecretResolver) ([]PlexLibrary, error) {
	var config serviceConnectionConfig
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return nil, errors.New("Plex configuration is not valid JSON")
	}
	if strings.TrimSpace(integration.SecretRef) == "" || resolveSecret == nil {
		return nil, errors.New("the Plex token is missing; save it first")
	}
	secret, err := resolveSecret(integration.SecretRef)
	if err != nil {
		return nil, errors.New("the Plex token could not be read; save it again")
	}
	token := strings.TrimSpace(secret)
	var wrapped struct {
		Token string `json:"token"`
	}
	if json.Unmarshal([]byte(secret), &wrapped) == nil && strings.TrimSpace(wrapped.Token) != "" {
		token = strings.TrimSpace(wrapped.Token)
	}
	client, err := plex.New(plex.Config{BaseURL: config.BaseURL, Token: token, AllowHTTP: config.AllowHTTP, Timeout: 15 * time.Second}, nil)
	if err != nil {
		return nil, errors.New("the Plex address is not valid (use https, or http with allow_http)")
	}
	sections, err := client.Sections(ctx)
	if err != nil {
		return nil, fmt.Errorf("Plex did not answer with its libraries: %w", err)
	}
	libraries := make([]PlexLibrary, 0, len(sections))
	for _, section := range sections {
		library := PlexLibrary{Key: section.Key, Title: section.Title, Type: section.Type, Locations: []string{}}
		for _, location := range section.Locations {
			library.Locations = append(library.Locations, location.Path)
		}
		libraries = append(libraries, library)
	}
	return libraries, nil
}

// CheckSeerr confirms a Seerr connection's saved API key is accepted.
func CheckSeerr(ctx context.Context, integration postgres.Integration, resolveSecret SecretResolver) error {
	var config serviceConnectionConfig
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return errors.New("Seerr configuration is not valid JSON")
	}
	if strings.TrimSpace(integration.SecretRef) == "" || resolveSecret == nil {
		return errors.New("the Seerr API key is missing; save it first")
	}
	key, err := resolveSecret(integration.SecretRef)
	if err != nil {
		return errors.New("the Seerr API key could not be read; save it again")
	}
	client, err := seerr.New(seerr.Config{BaseURL: config.BaseURL, APIKey: key, AllowHTTP: config.AllowHTTP, Timeout: 15 * time.Second}, nil)
	if err != nil {
		return errors.New("the Seerr address is not valid (use https, or http with allow_http)")
	}
	return client.CheckAccess(ctx)
}
