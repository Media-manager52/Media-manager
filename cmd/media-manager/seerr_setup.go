package main

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/media-manager/media-manager/internal/integrationsecrets"
	"github.com/media-manager/media-manager/internal/postgres"
)

// websiteSeerr is the Seerr intake configuration taken from the Seerr
// connection saved on the website.
type websiteSeerr struct {
	enabled       bool
	baseURL       string
	apiKey        string
	instanceID    string
	webhookSecret string
	allowHTTP     bool
}

type integrationLister interface {
	ListIntegrations(ctx context.Context) ([]postgres.Integration, error)
}

// seerrWebhookLabel names the derived webhook secret of a Seerr connection.
func seerrWebhookLabel(instanceID string) string {
	return "seerr-webhook:" + instanceID
}

// seerrFromWebsite uses the enabled default Seerr connection when no SEERR_*
// environment values are set. Its API key is resolved like every other
// connection secret, and its webhook secret is derived from the server key, so
// nothing extra is stored. A reason is returned when a saved connection
// cannot be used; Seerr intake then stays off and startup continues.
func seerrFromWebsite(ctx context.Context, store integrationLister, resolveSecret func(string) (string, error), secrets *integrationsecrets.Service) (websiteSeerr, string) {
	integrations, err := store.ListIntegrations(ctx)
	if err != nil {
		return websiteSeerr{}, "the Seerr connection could not be loaded"
	}
	for _, integration := range integrations {
		if integration.Kind != "seerr" || !integration.Enabled || !integration.Default {
			continue
		}
		var config struct {
			BaseURL   string `json:"base_url"`
			AllowHTTP bool   `json:"allow_http"`
		}
		if json.Unmarshal(integration.Config, &config) != nil || strings.TrimSpace(config.BaseURL) == "" {
			return websiteSeerr{}, "the Seerr connection has no address"
		}
		if strings.TrimSpace(integration.SecretRef) == "" {
			return websiteSeerr{}, "the Seerr connection has no API key"
		}
		if secrets == nil {
			return websiteSeerr{}, "set SECRET_ENCRYPTION_KEY so the Seerr webhook secret can be derived"
		}
		key, err := resolveSecret(integration.SecretRef)
		if err != nil || strings.TrimSpace(key) == "" {
			return websiteSeerr{}, "the Seerr API key could not be read"
		}
		instanceID := integration.ID.String()
		return websiteSeerr{enabled: true, baseURL: strings.TrimSpace(config.BaseURL), apiKey: strings.TrimSpace(key), instanceID: instanceID, webhookSecret: secrets.Derive(seerrWebhookLabel(instanceID)), allowHTTP: config.AllowHTTP}, ""
	}
	return websiteSeerr{}, ""
}
