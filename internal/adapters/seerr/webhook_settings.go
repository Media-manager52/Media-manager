package seerr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// WebhookPayloadTemplate is the body Media Manager's webhook endpoint parses
// (see ParseWebhook); Seerr fills the {{…}} variables.
const WebhookPayloadTemplate = `{"notification_type":"{{notification_type}}","request_id":"{{request_id}}","media_type":"{{media_type}}","media_tmdbid":"{{media_tmdbid}}","media_tvdbid":"{{media_tvdbid}}"}`

// WebhookTypes are the Seerr notification types Media Manager acts on:
// pending, approved, available, declined, auto-approved and auto-requested.
const WebhookTypes = 2 | 4 | 8 | 64 | 128 | 4096

// TestNotificationType is what Seerr sends when its webhook is tested.
const TestNotificationType = "TEST_NOTIFICATION"

// WebhookState describes Seerr's webhook without its authorization header.
type WebhookState struct {
	URL     string `json:"webhook_url"`
	Enabled bool   `json:"enabled"`
	// Managed is true when the webhook sends Media Manager's events, in Media
	// Manager's format, with Media Manager's secret.
	Managed bool `json:"managed"`
}

// WebhookSettings reads Seerr's webhook settings as Seerr returns them. The
// result contains the authorization header and must never leave the server.
func (client *Client) WebhookSettings(ctx context.Context) (map[string]any, error) {
	var settings map[string]any
	if err := client.settingsCall(ctx, http.MethodGet, "/api/v1/settings/notifications/webhook", nil, &settings); err != nil {
		return nil, err
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, nil
}

// TestWebhookSettings asks Seerr to send a test notification with settings,
// without saving them. Seerr answers 2xx only when the receiver did.
func (client *Client) TestWebhookSettings(ctx context.Context, settings map[string]any) error {
	return client.settingsCall(ctx, http.MethodPost, "/api/v1/settings/notifications/webhook/test", settings, nil)
}

// SaveWebhookSettings replaces Seerr's webhook settings.
func (client *Client) SaveWebhookSettings(ctx context.Context, settings map[string]any) error {
	return client.settingsCall(ctx, http.MethodPost, "/api/v1/settings/notifications/webhook", settings, nil)
}

// DescribeWebhook summarizes settings for the owner; the authorization header
// is only compared, never returned.
func DescribeWebhook(settings map[string]any, secret string) WebhookState {
	options, _ := settings["options"].(map[string]any)
	state := WebhookState{}
	state.Enabled, _ = settings["enabled"].(bool)
	state.URL, _ = options["webhookUrl"].(string)
	header, _ := options["authHeader"].(string)
	payload, _ := options["jsonPayload"].(string)
	types, _ := settings["types"].(float64)
	expected, actual := sha256.Sum256([]byte("Bearer "+secret)), sha256.Sum256([]byte(header))
	state.Managed = state.Enabled && len(secret) >= 32 && subtle.ConstantTimeCompare(expected[:], actual[:]) == 1 &&
		compactJSON(payload) == compactJSON(WebhookPayloadTemplate) && int64(types)&WebhookTypes == WebhookTypes
	return state
}

// WithMediaManagerWebhook returns settings that send Media Manager's events to
// webhookURL with secret. Other notification types and custom headers the
// owner set in Seerr are kept.
func WithMediaManagerWebhook(settings map[string]any, webhookURL, secret string) (map[string]any, error) {
	parsed, err := url.Parse(strings.TrimSpace(webhookURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("the webhook address must be an http:// or https:// address")
	}
	if len(secret) < 32 {
		return nil, errors.New("the Seerr webhook secret is not configured")
	}
	result := map[string]any{}
	for key, value := range settings {
		result[key] = value
	}
	options := map[string]any{}
	if existing, ok := settings["options"].(map[string]any); ok {
		for key, value := range existing {
			options[key] = value
		}
	}
	// Seerr stores the template as a JSON string literal.
	quoted, _ := json.Marshal(WebhookPayloadTemplate)
	options["webhookUrl"], options["jsonPayload"], options["authHeader"] = parsed.String(), string(quoted), "Bearer "+secret
	if _, ok := options["customHeaders"]; !ok {
		options["customHeaders"] = []any{}
	}
	types, _ := settings["types"].(float64)
	result["enabled"], result["types"], result["options"] = true, int64(types)|WebhookTypes, options
	if _, ok := result["embedPoster"]; !ok {
		result["embedPoster"] = false
	}
	return result, nil
}

func compactJSON(value string) string {
	var buffer bytes.Buffer
	if json.Compact(&buffer, []byte(value)) != nil {
		return value
	}
	return buffer.String()
}

func (client *Client) settingsCall(ctx context.Context, method, path string, body any, target any) error {
	endpoint := *client.base
	endpoint.Path = strings.TrimSuffix(client.base.Path, "/") + path
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return err
	}
	request.Header.Set("X-Api-Key", client.apiKey)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, client.responseLimit+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > client.responseLimit {
		return ErrResponseTooLarge
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return errors.New("Seerr refused the API key")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Seerr returned HTTP %d", response.StatusCode)
	}
	if target == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, target)
}
