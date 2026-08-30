package commissioning

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
)

type SecretResolver func(string) (string, error)

type NewznabService struct {
	Store         *postgres.Store
	Scheduler     *indexers.Scheduler
	ResolveSecret SecretResolver
	Transport     http.RoundTripper
}

type config struct {
	BaseURL           string `json:"base_url"`
	AllowHTTP         bool   `json:"allow_http"`
	MinimumIntervalMS int64  `json:"minimum_interval_ms"`
}

func (service *NewznabService) Commission(ctx context.Context, integrationID uuid.UUID) (postgres.Integration, error) {
	integration, err := service.Store.Integration(ctx, integrationID)
	if err != nil {
		return postgres.Integration{}, err
	}
	var values config
	if integration.Kind != "newznab" || json.Unmarshal(integration.Config, &values) != nil || service.Scheduler == nil || service.ResolveSecret == nil {
		return postgres.Integration{}, errors.New("Newznab commissioning is not configured")
	}
	var persisted map[string]any
	_ = json.Unmarshal(integration.Config, &persisted)
	if verified, _ := persisted["mode_verified"].(bool); integration.Enabled && verified {
		return integration, nil
	}
	secret, err := service.ResolveSecret(integration.SecretRef)
	if err != nil {
		return postgres.Integration{}, err
	}
	minimum := time.Duration(values.MinimumIntervalMS) * time.Millisecond
	if minimum < 3*time.Second {
		minimum = 3 * time.Second
	}
	if err := service.Scheduler.Configure(integration.ID.String(), indexers.Budget{MinimumInterval: minimum, FailureBackoff: 15 * time.Second, MaximumBackoff: 30 * time.Minute}); err != nil {
		return postgres.Integration{}, err
	}
	client, err := newznab.New(newznab.Config{IndexerID: integration.ID.String(), BaseURL: values.BaseURL, APIKey: secret, AllowHTTP: values.AllowHTTP, Timeout: 20 * time.Second}, service.Scheduler, service.Transport)
	if err != nil {
		return postgres.Integration{}, err
	}
	probeContext, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	caps, err := client.Caps(probeContext)
	if err != nil {
		return postgres.Integration{}, err
	}
	genericSearch := false
	for _, capability := range caps.Searches {
		if capability.Mode == "search" && capability.Available {
			genericSearch = true
			break
		}
	}
	if !genericSearch {
		return postgres.Integration{}, errors.New("Newznab CAPS does not advertise general search")
	}
	query := "media-manager-commission-" + uuid.NewV7().String()
	observations, err := client.CommissionSearch(probeContext, newznab.SearchRequest{Mode: "search", Params: []newznab.Param{{Name: "q", Value: query}}, Limit: 1})
	if err != nil {
		return postgres.Integration{}, err
	}
	if len(observations) != 0 {
		return postgres.Integration{}, errors.New("random negative Newznab query unexpectedly returned a release")
	}
	return service.Store.CompleteNewznabCommission(ctx, integration.ID, caps, time.Now().UTC())
}
