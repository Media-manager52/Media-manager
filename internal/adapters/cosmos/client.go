// Package cosmos talks to the Cosmos server that runs Media Manager, through
// its API with an API token the owner pastes for one action. The token is
// only held in memory for that action.
package cosmos

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config says how to reach Cosmos.
type Config struct {
	// Host is the name Cosmos is opened at, such as "media.example.com". Its
	// certificate is checked against this name.
	Host string
	// DialAddress, when set, is where connections go instead of Host's own
	// address, such as "host.docker.internal:443" from a container on the
	// Cosmos server.
	DialAddress string
	APIKey      string
	Timeout     time.Duration
	// TLS overrides the TLS settings; tests use it.
	TLS *tls.Config
}

// Client is a Cosmos API client.
type Client struct {
	base   url.URL
	apiKey string
	http   *http.Client
}

var (
	ErrInvalidConfig = errors.New("invalid Cosmos configuration")
	// ErrUnauthorized means Cosmos refused the API token.
	ErrUnauthorized = errors.New("Cosmos did not accept the API token")
)

// New makes a client.
func New(config Config) (*Client, error) {
	host := strings.TrimSpace(config.Host)
	if host == "" || strings.ContainsAny(host, "/?#@ ") || strings.TrimSpace(config.APIKey) == "" {
		return nil, ErrInvalidConfig
	}
	if config.Timeout <= 0 {
		config.Timeout = 60 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if config.TLS != nil {
		transport.TLSClientConfig = config.TLS
	}
	if dial := strings.TrimSpace(config.DialAddress); dial != "" {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, dial)
		}
	}
	return &Client{
		base:   url.URL{Scheme: "https", Host: host},
		apiKey: config.APIKey,
		http: &http.Client{Transport: transport, Timeout: config.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("Cosmos redirects are not followed")
		}},
	}, nil
}

// ContainerSummary is one container in Cosmos' list.
type ContainerSummary struct {
	ID              string   `json:"Id"`
	Names           []string `json:"Names"`
	Image           string   `json:"Image"`
	State           string   `json:"State"`
	NetworkSettings struct {
		Networks map[string]Network `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Network is a container's place on one Docker network.
type Network struct {
	IPAddress string   `json:"IPAddress"`
	Aliases   []string `json:"Aliases"`
	DNSNames  []string `json:"DNSNames"`
}

// Name is the container's name without Docker's leading slash.
func (container ContainerSummary) Name() string {
	if len(container.Names) == 0 {
		return ""
	}
	return strings.TrimPrefix(container.Names[0], "/")
}

// Container is one container's settings, as far as Media Manager needs them.
type Container struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Image  string `json:"Image"`
	Config struct {
		Hostname string `json:"Hostname"`
		Image    string `json:"Image"`
	} `json:"Config"`
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
		Health  *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	HostConfig struct {
		Binds    []string          `json:"Binds"`
		Mounts   []json.RawMessage `json:"Mounts"`
		Memory   int64             `json:"Memory"`
		NanoCpus int64             `json:"NanoCpus"`
	} `json:"HostConfig"`
	// Mounts is what the container has mounted now.
	Mounts          []MountPoint `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]Network `json:"Networks"`
	} `json:"NetworkSettings"`
}

// MountPoint is one mounted folder or volume as Docker reports it.
type MountPoint struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
	Propagation string `json:"Propagation"`
}

// Healthy reports whether the container runs and its health check, if it
// has one, passes.
func (container Container) Healthy() bool {
	if !container.State.Running {
		return false
	}
	return container.State.Health == nil || container.State.Health.Status == "" || container.State.Health.Status == "healthy"
}

// Me checks the token.
func (client *Client) Me(ctx context.Context) error {
	return client.call(ctx, http.MethodGet, "/cosmos/api/me", nil, nil)
}

// Containers lists every container.
func (client *Client) Containers(ctx context.Context) ([]ContainerSummary, error) {
	var containers []ContainerSummary
	err := client.call(ctx, http.MethodGet, "/cosmos/api/servapps", nil, &containers)
	return containers, err
}

// Container reads one container by name or ID.
func (client *Client) Container(ctx context.Context, name string) (Container, error) {
	var container Container
	if !validName(name) {
		return container, ErrInvalidConfig
	}
	err := client.call(ctx, http.MethodGet, "/cosmos/api/servapps/"+url.PathEscape(name)+"/", nil, &container)
	return container, err
}

// UpdateMounts replaces a container's mounts and recreates it, keeping its
// memory and CPU limits (Cosmos clears them when they are not sent). Cosmos
// puts the old container back when the new one cannot be created.
func (client *Client) UpdateMounts(ctx context.Context, name string, mounts []Mount, memory, nanoCPUs int64) error {
	if !validName(name) || len(mounts) == 0 {
		return ErrInvalidConfig
	}
	form := map[string]any{"Volumes": mounts}
	if memory > 0 {
		form["memLimit"] = strconv.FormatInt(memory, 10)
	}
	if nanoCPUs > 0 {
		form["cpus"] = float64(nanoCPUs) / 1e9
	}
	return client.call(ctx, http.MethodPost, "/cosmos/api/servapps/"+url.PathEscape(name)+"/update", form, nil)
}

func validName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, character := range name {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.') {
			return false
		}
	}
	return true
}

func (client *Client) call(ctx context.Context, method, path string, input, output any) error {
	endpoint := client.base
	endpoint.Path = path
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		var unknownAuthority x509.UnknownAuthorityError
		var invalidName x509.HostnameError
		if errors.As(err, &unknownAuthority) || errors.As(err, &invalidName) {
			return errors.New("Cosmos' certificate is not trusted")
		}
		return errors.New("Cosmos could not be reached at " + client.base.Host)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &problem)
		if problem.Message != "" {
			return fmt.Errorf("Cosmos returned HTTP %d: %s", response.StatusCode, truncate(problem.Message, 300))
		}
		return fmt.Errorf("Cosmos returned HTTP %d", response.StatusCode)
	}
	if output == nil {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Data) == 0 {
		return errors.New("Cosmos returned an unexpected answer")
	}
	return json.Unmarshal(envelope.Data, output)
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
