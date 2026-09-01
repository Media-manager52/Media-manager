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
	QueryLimit        int    `json:"query_limit"`
	GrabLimit         int    `json:"grab_limit"`
	BudgetWindow      int64  `json:"budget_window_seconds"`
}

const maximumCommissionPacingWait = 10 * time.Second

func (service *NewznabService) Commission(ctx context.Context, integrationID uuid.UUID) (postgres.Integration, error) {
	integration, err := service.Store.Integration(ctx, integrationID)
	if err != nil {
		return postgres.Integration{}, err
	}
	var values config
	if integration.Kind != "newznab" || json.Unmarshal(integration.Config, &values) != nil || service.Scheduler == nil || service.ResolveSecret == nil {
		return postgres.Integration{}, errors.New("Newznab commissioning is not configured")
	}
	secret, err := service.ResolveSecret(integration.SecretRef)
	if err != nil {
		return postgres.Integration{}, err
	}
	budget := indexers.NewznabBudget(values.MinimumIntervalMS, values.QueryLimit, values.GrabLimit, values.BudgetWindow)
	if err := service.Scheduler.Configure(integration.ID.String(), budget); err != nil {
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
	dialects, err := newznab.SelectSearchDialects(caps)
	if err != nil {
		return postgres.Integration{}, err
	}
	query := "media-manager-commission-" + uuid.NewV7().String()
	validationDialect := dialects.Series
	observations, err := client.CommissionSearch(probeContext, newznab.SearchRequest{Mode: validationDialect.Mode, Params: []newznab.Param{{Name: "q", Value: query}}, Limit: 1})
	if delay, ok := commissionPacingDelay(err, time.Now().UTC()); ok {
		timer := time.NewTimer(delay)
		select {
		case <-probeContext.Done():
			timer.Stop()
			return postgres.Integration{}, probeContext.Err()
		case <-timer.C:
		}
		observations, err = client.CommissionSearch(probeContext, newznab.SearchRequest{Mode: validationDialect.Mode, Params: []newznab.Param{{Name: "q", Value: query}}, Limit: 1})
	}
	if err != nil {
		return postgres.Integration{}, err
	}
	if len(observations) != 0 {
		return postgres.Integration{}, errors.New("random negative Newznab query unexpectedly returned a release")
	}
	return service.Store.CompleteNewznabCommission(ctx, integration.ID, caps, dialects, time.Now().UTC())
}

func commissionPacingDelay(err error, now time.Time) (time.Duration, bool) {
	var pacing indexers.CooldownError
	if !errors.As(err, &pacing) {
		return 0, false
	}
	delay := pacing.RetryAt().Sub(now.UTC())
	if delay < 0 {
		delay = 0
	}
	if delay > maximumCommissionPacingWait {
		return 0, false
	}
	return delay, true
}
