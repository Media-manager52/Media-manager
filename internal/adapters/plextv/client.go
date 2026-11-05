// Package plextv signs in to a Plex account with Plex's PIN login (the flow
// Seerr and Tautulli use) and lists the account's Plex Media Servers with the
// addresses they advertise. The owner approves the login on app.plex.tv; the
// account token never reaches the browser.
package plextv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://plex.tv"
	authAppURL     = "https://app.plex.tv/auth"
	product        = "Media Manager"
	responseLimit  = 4 << 20
)

var ErrInvalidPIN = errors.New("the Plex login is unknown or expired")

// Client talks to plex.tv for one client identifier. The identifier names
// this Media Manager as a device on the Plex account; the PIN and its token
// are bound to it.
type Client struct {
	base             *url.URL
	clientIdentifier string
	http             *http.Client
}

// New returns a plex.tv client. baseURL is for tests ("" means plex.tv).
func New(clientIdentifier, baseURL string, transport http.RoundTripper) (*Client, error) {
	clientIdentifier = strings.TrimSpace(clientIdentifier)
	if clientIdentifier == "" || len(clientIdentifier) > 128 {
		return nil, errors.New("a Plex client identifier is required")
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return nil, errors.New("invalid plex.tv address")
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{base: base, clientIdentifier: clientIdentifier, http: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// PIN is one pending Plex login.
type PIN struct {
	ID        int64     `json:"id"`
	Code      string    `json:"code"`
	AuthToken string    `json:"authToken"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Connection is one address a Plex Media Server advertises.
type Connection struct {
	URI      string `json:"uri"`
	Protocol string `json:"protocol"`
	Local    bool   `json:"local"`
	Relay    bool   `json:"relay"`
	IPv6     bool   `json:"IPv6"`
}

// Server is a Plex Media Server the account can use.
type Server struct {
	Name             string       `json:"name"`
	ClientIdentifier string       `json:"clientIdentifier"`
	Owned            bool         `json:"owned"`
	Provides         string       `json:"provides"`
	AccessToken      string       `json:"accessToken"`
	Connections      []Connection `json:"connections"`
}

// CreatePIN starts a login.
func (client *Client) CreatePIN(ctx context.Context) (PIN, error) {
	var pin PIN
	if err := client.call(ctx, http.MethodPost, "/api/v2/pins", url.Values{"strong": {"true"}}, "", &pin); err != nil {
		return PIN{}, err
	}
	if pin.ID <= 0 || strings.TrimSpace(pin.Code) == "" {
		return PIN{}, errors.New("plex.tv did not return a login code")
	}
	return pin, nil
}

// CheckPIN returns the PIN; AuthToken is set once the owner approved it.
func (client *Client) CheckPIN(ctx context.Context, id int64) (PIN, error) {
	if id <= 0 {
		return PIN{}, ErrInvalidPIN
	}
	var pin PIN
	if err := client.call(ctx, http.MethodGet, "/api/v2/pins/"+strconv.FormatInt(id, 10), nil, "", &pin); err != nil {
		return PIN{}, err
	}
	return pin, nil
}

// AuthURL is where the owner approves the login.
func (client *Client) AuthURL(code string) string {
	values := url.Values{}
	values.Set("clientID", client.clientIdentifier)
	values.Set("code", code)
	values.Set("context[device][product]", product)
	return authAppURL + "#?" + values.Encode()
}

// Servers lists the Plex Media Servers on the account.
func (client *Client) Servers(ctx context.Context, token string) ([]Server, error) {
	var resources []Server
	if err := client.call(ctx, http.MethodGet, "/api/v2/resources", url.Values{"includeHttps": {"1"}, "includeRelay": {"1"}}, token, &resources); err != nil {
		return nil, err
	}
	servers := []Server{}
	for _, resource := range resources {
		if !strings.Contains(","+resource.Provides+",", ",server,") || strings.TrimSpace(resource.AccessToken) == "" {
			continue
		}
		servers = append(servers, resource)
	}
	return servers, nil
}

func (client *Client) call(ctx context.Context, method, path string, query url.Values, token string, target any) error {
	endpoint := *client.base
	endpoint.Path = strings.TrimSuffix(client.base.Path, "/") + path
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Plex-Product", product)
	request.Header.Set("X-Plex-Client-Identifier", client.clientIdentifier)
	if token != "" {
		request.Header.Set("X-Plex-Token", token)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return errors.New("plex.tv could not be reached")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return ErrInvalidPIN
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("plex.tv returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil || len(body) > responseLimit {
		return errors.New("plex.tv answered with an unreadable response")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return errors.New("plex.tv answered with an unreadable response")
	}
	return nil
}

// Identity reads a Plex Media Server's machine identifier at uri, proving
// both that Media Manager reaches it and which server answers there.
func Identity(ctx context.Context, uri, token string, transport http.RoundTripper) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return "", errors.New("invalid Plex address")
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := *parsed
	endpoint.Path = strings.TrimSuffix(parsed.Path, "/") + "/identity"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Plex-Token", token)
	response, err := client.Do(request)
	if err != nil {
		return "", errors.New("not reachable from Media Manager")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Plex returned HTTP %d", response.StatusCode)
	}
	var document struct {
		MediaContainer struct {
			MachineIdentifier string `json:"machineIdentifier"`
		} `json:"MediaContainer"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil || json.Unmarshal(body, &document) != nil || document.MediaContainer.MachineIdentifier == "" {
		return "", errors.New("the address did not answer as a Plex Media Server")
	}
	return document.MediaContainer.MachineIdentifier, nil
}
