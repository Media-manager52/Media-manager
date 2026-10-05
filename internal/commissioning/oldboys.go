package commissioning

import (
	"context"
	"errors"
	"net/http"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/oldboys"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

type OldBoysService struct {
	Store       *postgres.Store
	Scheduler   *indexers.Scheduler
	Credentials *oldboyscredentials.Service
	Transport   http.RoundTripper
}

// Commission runs exactly four bounded filter controls and one NZB retrieval.
// The NZB is validated in memory and discarded; no acquisition is submitted.
func (service *OldBoysService) Commission(ctx context.Context) (postgres.Integration, error) {
	if service.Credentials == nil || service.Scheduler == nil {
		return postgres.Integration{}, errors.New("OldBoys commissioning is unavailable")
	}
	credentials, stamp, err := service.Credentials.LoadVersioned(ctx)
	if err != nil {
		return postgres.Integration{}, err
	}
	integration, err := service.Store.PrepareOldBoysCommission(ctx, stamp)
	if err != nil {
		return postgres.Integration{}, err
	}
	service.Scheduler.SetCommissioningGateSource(integration.ID.String(), service.Store)
	if err := service.Scheduler.Configure(integration.ID.String(), indexers.NewznabBudget(5000, 1000, 100, 86400)); err != nil {
		return postgres.Integration{}, err
	}
	guard := func(ctx context.Context) error { return service.Store.ValidateOldBoysSnapshot(ctx, integration, true) }
	client, err := oldboys.New(integration.ID, credentials, service.Scheduler, service.Transport, guard)
	if err != nil {
		return postgres.Integration{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	negative := "mmnegative" + uuid.NewV7().String() + " DANiSH"
	var selected string
	for _, spec := range []struct {
		name, title string
		category    int
		positive    bool
	}{{"Operation X DANiSH", "Operation X", 2, true}, {negative, "", 2, false}, {"Klovn DANiSH", "Klovn", 1, true}, {negative, "", 1, false}} {
		if err := waitOldBoysPacing(ctx, service.Scheduler, integration.ID.String()); err != nil {
			return postgres.Integration{}, err
		}
		observations, total, err := client.ProbeSearch(ctx, oldboys.SearchRequest{Name: spec.name, Category: spec.category, Page: 1}, indexers.WorkloadCommissioning, nil)
		if err != nil {
			return postgres.Integration{}, err
		}
		if !spec.positive && total != 0 {
			return postgres.Integration{}, errors.New("OldBoys negative control returned releases; provider remains disabled")
		}
		if spec.positive {
			matched := false
			for _, observation := range observations {
				if releases.TitleMatches(observation.Title, spec.title) {
					matched = true
					if selected == "" && spec.category == 2 {
						selected = observation.NativeGUID
					}
				}
			}
			if !matched {
				return postgres.Integration{}, errors.New("OldBoys positive control returned no matching Danish release; provider remains disabled")
			}
		}
	}
	if err := waitOldBoysPacing(ctx, service.Scheduler, integration.ID.String()); err != nil {
		return postgres.Integration{}, err
	}
	if _, err := client.GetNZBPackageGuarded(ctx, selected, nil); err != nil {
		return postgres.Integration{}, err
	}
	return service.Store.CompleteOldBoysCommission(ctx, integration, time.Now().UTC())
}

func waitOldBoysPacing(ctx context.Context, scheduler *indexers.Scheduler, id string) error {
	snapshot := scheduler.Snapshot(id)
	until := snapshot.NextAllowedAt
	if snapshot.CooldownUntil.After(until) {
		until = snapshot.CooldownUntil
	}
	delay := time.Until(until)
	if delay <= 0 {
		return nil
	}
	if delay > maximumCommissionPacingWait {
		return indexers.CooldownError{Until: until}
	}
	timer := time.NewTimer(delay + 10*time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
