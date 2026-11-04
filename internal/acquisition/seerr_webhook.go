package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/postgres"
)

// SeerrWebhookState is Seerr's webhook as the website shows it. The
// authorization header is never part of it.
type SeerrWebhookState struct {
	URL     string `json:"webhook_url"`
	Enabled bool   `json:"enabled"`
	Managed bool   `json:"managed"`
}

func seerrClientFor(integration postgres.Integration, resolveSecret SecretResolver) (*seerr.Client, error) {
	var config serviceConnectionConfig
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return nil, errors.New("Seerr configuration is not valid JSON")
	}
	if strings.TrimSpace(integration.SecretRef) == "" || resolveSecret == nil {
		return nil, errors.New("the Seerr API key is missing; save it first")
	}
	key, err := resolveSecret(integration.SecretRef)
	if err != nil {
		return nil, errors.New("the Seerr API key could not be read; save it again")
	}
	client, err := seerr.New(seerr.Config{BaseURL: config.BaseURL, APIKey: key, AllowHTTP: config.AllowHTTP, Timeout: 15 * time.Second}, nil)
	if err != nil {
		return nil, errors.New("the Seerr address is not valid (use https, or http with allow_http)")
	}
	return client, nil
}

// SeerrWebhook reads Seerr's webhook. Managed is only true when secret is the
// running intake's webhook secret and Seerr sends it.
func SeerrWebhook(ctx context.Context, integration postgres.Integration, resolveSecret SecretResolver, secret string) (SeerrWebhookState, error) {
	client, err := seerrClientFor(integration, resolveSecret)
	if err != nil {
		return SeerrWebhookState{}, err
	}
	settings, err := client.WebhookSettings(ctx)
	if err != nil {
		return SeerrWebhookState{}, fmt.Errorf("Seerr did not return its webhook settings: %w", err)
	}
	state := seerr.DescribeWebhook(settings, secret)
	return SeerrWebhookState{URL: state.URL, Enabled: state.Enabled, Managed: state.Managed}, nil
}

// ConfigureSeerrWebhook points Seerr's webhook at webhookURL with secret. Seerr
// first sends a test notification with the new settings; they are saved only
// when Media Manager answered it, so a wrong address never replaces a working
// webhook. The saved settings are read back before success is reported.
func ConfigureSeerrWebhook(ctx context.Context, integration postgres.Integration, resolveSecret SecretResolver, secret, webhookURL string) (SeerrWebhookState, error) {
	client, err := seerrClientFor(integration, resolveSecret)
	if err != nil {
		return SeerrWebhookState{}, err
	}
	current, err := client.WebhookSettings(ctx)
	if err != nil {
		return SeerrWebhookState{}, fmt.Errorf("Seerr did not return its webhook settings: %w", err)
	}
	next, err := seerr.WithMediaManagerWebhook(current, webhookURL, secret)
	if err != nil {
		return SeerrWebhookState{}, err
	}
	if err := client.TestWebhookSettings(ctx, next); err != nil {
		return SeerrWebhookState{}, fmt.Errorf("Seerr could not deliver a test notification to that address, so nothing was changed in Seerr: %w", err)
	}
	if err := client.SaveWebhookSettings(ctx, next); err != nil {
		return SeerrWebhookState{}, fmt.Errorf("Seerr did not save the webhook: %w", err)
	}
	saved, err := client.WebhookSettings(ctx)
	if err != nil {
		return SeerrWebhookState{}, fmt.Errorf("Seerr saved the webhook but could not be read back: %w", err)
	}
	state := seerr.DescribeWebhook(saved, secret)
	if !state.Managed {
		return SeerrWebhookState{URL: state.URL, Enabled: state.Enabled}, errors.New("Seerr saved the webhook, but reading it back did not show Media Manager's settings")
	}
	return SeerrWebhookState{URL: state.URL, Enabled: state.Enabled, Managed: true}, nil
}
