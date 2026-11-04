package main

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/integrationsecrets"
	"github.com/media-manager/media-manager/internal/postgres"
)

type listedIntegrations []postgres.Integration

func (items listedIntegrations) ListIntegrations(context.Context) ([]postgres.Integration, error) {
	return items, nil
}

type noSecrets struct{}

func (noSecrets) SaveIntegrationSecret(context.Context, uuid.UUID, []byte, string, time.Time) error {
	return nil
}
func (noSecrets) LoadIntegrationSecret(context.Context, uuid.UUID) ([]byte, error) {
	return nil, integrationsecrets.ErrNotStored
}

// TestSeerrFromWebsiteUsesTheDefaultConnection proves startup takes Seerr
// intake from the website's default Seerr connection, derives a stable
// webhook secret, and stays off with a reason when the connection is not
// usable.
func TestSeerrFromWebsiteUsesTheDefaultConnection(t *testing.T) {
	secrets, err := integrationsecrets.New(noSecrets{}, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewV7()
	seerr := postgres.Integration{ID: id, Kind: "seerr", Name: "Seerr", Enabled: true, Default: true, SecretRef: integrationsecrets.Reference(id), Config: []byte(`{"base_url":"http://seerr:5055","allow_http":true}`)}
	resolve := func(reference string) (string, error) {
		if reference == integrationsecrets.Reference(id) {
			return "seerr-key", nil
		}
		return "", errors.New("unknown")
	}
	got, reason := seerrFromWebsite(context.Background(), listedIntegrations{{Kind: "plex", Enabled: true, Default: true}, seerr}, resolve, secrets)
	if !got.enabled || reason != "" || got.baseURL != "http://seerr:5055" || !got.allowHTTP || got.apiKey != "seerr-key" || got.instanceID != id.String() || len(got.webhookSecret) != 64 || got.webhookSecret != secrets.Derive("seerr-webhook:"+id.String()) {
		t.Fatalf("website Seerr = %+v reason=%q", got, reason)
	}
	if got, reason := seerrFromWebsite(context.Background(), listedIntegrations{}, resolve, secrets); got.enabled || reason != "" {
		t.Fatalf("no Seerr connection must leave intake off silently: %+v %q", got, reason)
	}
	keyless := seerr
	keyless.SecretRef = ""
	if got, reason := seerrFromWebsite(context.Background(), listedIntegrations{keyless}, resolve, secrets); got.enabled || reason == "" {
		t.Fatalf("a Seerr connection without a key must stay off with a reason: %+v", got)
	}
	if got, reason := seerrFromWebsite(context.Background(), listedIntegrations{seerr}, resolve, nil); got.enabled || reason == "" {
		t.Fatalf("without a server key there is no webhook secret: %+v", got)
	}
	disabled := seerr
	disabled.Enabled = false
	if got, _ := seerrFromWebsite(context.Background(), listedIntegrations{disabled}, resolve, secrets); got.enabled {
		t.Fatal("a disabled Seerr connection was used")
	}
}
