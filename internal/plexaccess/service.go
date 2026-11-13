package plexaccess

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"

	"github.com/media-manager/media-manager/internal/adapters/cosmos"
)

// Service opens Cosmos for one fix. Tests replace its parts.
type Service struct {
	// OpenCosmos connects to Cosmos at host (the name the owner opens Media
	// Manager at) with the pasted API token and checks the token.
	OpenCosmos func(ctx context.Context, host, apiKey string) (Cosmos, error)
	// Self is Media Manager's own container.
	Self func() (string, error)
}

// NewService uses the real Cosmos and the container's hostname.
func NewService() *Service {
	return &Service{OpenCosmos: openCosmos, Self: os.Hostname}
}

// ErrCosmosUnauthorized means Cosmos refused the pasted token.
var ErrCosmosUnauthorized = cosmos.ErrUnauthorized

// openCosmos reaches Cosmos on the server Media Manager runs on first (the
// Cosmos install gives the API host.docker.internal), then by the name's
// own address.
func openCosmos(ctx context.Context, host, apiKey string) (Cosmos, error) {
	host = strings.TrimSpace(host)
	port := "443"
	if _, given, err := net.SplitHostPort(host); err == nil && given != "" {
		port = given
	}
	var firstErr error
	if addresses, err := net.DefaultResolver.LookupHost(ctx, "host.docker.internal"); err == nil && len(addresses) > 0 {
		client, err := cosmos.New(cosmos.Config{Host: host, DialAddress: net.JoinHostPort("host.docker.internal", port), APIKey: apiKey})
		if err != nil {
			return nil, err
		}
		if firstErr = client.Me(ctx); firstErr == nil {
			return client, nil
		}
		if errors.Is(firstErr, cosmos.ErrUnauthorized) {
			return nil, firstErr
		}
	}
	client, err := cosmos.New(cosmos.Config{Host: host, APIKey: apiKey})
	if err != nil {
		return nil, err
	}
	if err := client.Me(ctx); err != nil {
		return nil, err
	}
	return client, nil
}
