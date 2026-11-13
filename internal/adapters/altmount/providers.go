package altmount

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Provider is one Usenet (NNTP) server as AltMount reports it. AltMount never
// returns the password; PasswordSet says whether one is stored.
type Provider struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Host                  string `json:"host"`
	Port                  int    `json:"port"`
	Username              string `json:"username"`
	MaxConnections        int    `json:"max_connections"`
	TLS                   bool   `json:"tls"`
	InsecureTLS           bool   `json:"insecure_tls"`
	PasswordSet           bool   `json:"password_set"`
	Enabled               bool   `json:"enabled"`
	IsBackupProvider      bool   `json:"is_backup_provider"`
	LastRTTMs             int64  `json:"last_rtt_ms"`
	QuotaBytes            int64  `json:"quota_bytes"`
	QuotaPeriodHours      int    `json:"quota_period_hours"`
	AccountExpirationDate string `json:"account_expiration_date"`
}

// ProviderInput creates a server (every field) or changes one (only the
// fields that are set). A nil or empty Password on a change keeps the stored
// password: AltMount would otherwise store the empty string.
type ProviderInput struct {
	Name                  *string `json:"name,omitempty"`
	Host                  *string `json:"host,omitempty"`
	Port                  *int    `json:"port,omitempty"`
	Username              *string `json:"username,omitempty"`
	Password              *string `json:"password,omitempty"`
	MaxConnections        *int    `json:"max_connections,omitempty"`
	TLS                   *bool   `json:"tls,omitempty"`
	InsecureTLS           *bool   `json:"insecure_tls,omitempty"`
	Enabled               *bool   `json:"enabled,omitempty"`
	IsBackupProvider      *bool   `json:"is_backup_provider,omitempty"`
	QuotaBytes            *int64  `json:"quota_bytes,omitempty"`
	QuotaPeriodHours      *int    `json:"quota_period_hours,omitempty"`
	AccountExpirationDate *string `json:"account_expiration_date,omitempty"`
}

// ProviderTest is a login to try before it is saved.
type ProviderTest struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	TLS         bool   `json:"tls"`
	InsecureTLS bool   `json:"insecure_tls"`
}

// ProviderTestResult is AltMount's answer to a test login.
type ProviderTestResult struct {
	Success      bool   `json:"success"`
	ErrorMessage string `json:"error_message,omitempty"`
	RTTMs        int64  `json:"rtt_ms,omitempty"`
}

// ErrInvalidProviderID is returned for an ID AltMount could not have issued.
var ErrInvalidProviderID = errors.New("invalid AltMount provider ID")

var providerIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// ListProviders reads the servers from AltMount's configuration. Only the
// provider list is decoded; the rest of the configuration (which carries
// AltMount's own API key) is discarded unread.
func (client *HTTPClient) ListProviders(ctx context.Context) ([]Provider, error) {
	var config struct {
		Providers []Provider `json:"providers"`
	}
	if err := client.providerJSON(ctx, http.MethodGet, "/api/config", nil, &config); err != nil {
		return nil, err
	}
	if config.Providers == nil {
		config.Providers = []Provider{}
	}
	return config.Providers, nil
}

// CreateProvider adds a server.
func (client *HTTPClient) CreateProvider(ctx context.Context, input ProviderInput) (Provider, error) {
	var created Provider
	err := client.providerJSON(ctx, http.MethodPost, "/api/providers", input, &created)
	return created, err
}

// UpdateProvider changes only the fields that are set.
func (client *HTTPClient) UpdateProvider(ctx context.Context, id string, input ProviderInput) (Provider, error) {
	if !providerIDPattern.MatchString(id) {
		return Provider{}, ErrInvalidProviderID
	}
	if input.Password != nil && *input.Password == "" {
		input.Password = nil
	}
	var updated Provider
	err := client.providerJSON(ctx, http.MethodPut, "/api/providers/"+url.PathEscape(id), input, &updated)
	return updated, err
}

// DeleteProvider removes a server.
func (client *HTTPClient) DeleteProvider(ctx context.Context, id string) error {
	if !providerIDPattern.MatchString(id) {
		return ErrInvalidProviderID
	}
	return client.providerJSON(ctx, http.MethodDelete, "/api/providers/"+url.PathEscape(id), nil, nil)
}

// ReorderProviders sets the order servers are tried in. AltMount requires
// every server's ID exactly once.
func (client *HTTPClient) ReorderProviders(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if !providerIDPattern.MatchString(id) {
			return ErrInvalidProviderID
		}
	}
	return client.providerJSON(ctx, http.MethodPut, "/api/providers/reorder", map[string][]string{"provider_ids": ids}, nil)
}

// TestProvider has AltMount try a login without saving it.
func (client *HTTPClient) TestProvider(ctx context.Context, test ProviderTest) (ProviderTestResult, error) {
	var result ProviderTestResult
	err := client.providerJSON(ctx, http.MethodPost, "/api/providers/test", test, &result)
	return result, err
}

// providerJSON is json with AltMount's refusal reason kept: provider changes
// are validated by AltMount, and its message ("Valid port is required") is
// what the owner needs to see. Each request is sent once (again only after a
// fresh login), so a change is never applied twice. The message never carries
// a password.
func (client *HTTPClient) providerJSON(ctx context.Context, method, path string, input, output any) error {
	var body []byte
	contentType := ""
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body, contentType = encoded, "application/json"
	}
	status, response, err := client.raw(ctx, method, path, contentType, body)
	if err == nil && status == http.StatusUnauthorized {
		if _, loginErr := client.Login(ctx); loginErr != nil {
			return loginErr
		}
		status, response, err = client.raw(ctx, method, path, contentType, body)
	}
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		apiError := APIError{StatusCode: status}
		if status >= 400 && status < 500 {
			apiError.Message = refusalMessage(response)
		}
		return apiError
	}
	if output == nil || len(bytes.TrimSpace(response)) == 0 {
		return nil
	}
	return decodeEnvelope(response, output)
}

// refusalMessage reads AltMount's {"error":{"message","details"}} answer.
func refusalMessage(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Details string `json:"details"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	message := strings.TrimSpace(envelope.Error.Message)
	if message == "" {
		message = strings.TrimSpace(envelope.Message)
	}
	if details := strings.TrimSpace(envelope.Error.Details); details != "" && message != "" && !strings.EqualFold(details, message) && strings.ToUpper(details) != details {
		message += ": " + details
	}
	if len(message) > 300 {
		message = message[:300]
	}
	return message
}
