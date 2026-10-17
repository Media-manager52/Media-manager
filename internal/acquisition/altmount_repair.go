package acquisition

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/postgres"
)

// AltMountRepairService turns AltMount's corrupted-file reports into repairs.
// AltMount's own repair asks Radarr/Sonarr to replace a broken file; Media
// Manager is the acquisition system here, so it acquires the replacement
// itself (see Store.PlanAltMountRepair). AltMount content is never modified.
type AltMountRepairService struct {
	Store         *postgres.Store
	ResolveSecret SecretResolver
	Transport     http.RoundTripper
	// ListCorrupted overrides the AltMount client in tests.
	ListCorrupted func(ctx context.Context, integration postgres.Integration) ([]altmount.HealthItem, error)
}

// HandleSweep plans a repair for every corrupted file AltMount reports for
// one AltMount client. Files outside Media Manager's AltMount category are
// not Media Manager publications and are skipped.
func (service *AltMountRepairService) HandleSweep(ctx context.Context, event postgres.OutboxEvent) error {
	var payload struct {
		IntegrationID string `json:"integration_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	integrationID, err := uuid.Parse(payload.IntegrationID)
	if err != nil {
		return errors.New("AltMount repair sweep has no valid integration")
	}
	integration, err := service.Store.Integration(ctx, integrationID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if integration.Kind != "altmount" || !integration.Enabled {
		return nil
	}
	var config altMountConfig
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return err
	}
	list := service.ListCorrupted
	if list == nil {
		list = service.listCorrupted
	}
	items, err := list(ctx, integration)
	if err != nil {
		return err
	}
	prefix := strings.Trim(strings.TrimSpace(config.Category), "/")
	counts := map[string]int{}
	for _, item := range items {
		path := strings.Trim(strings.TrimSpace(item.FilePath), "/")
		if prefix != "" && !strings.HasPrefix(path, prefix+"/") {
			counts["outside_category"]++
			continue
		}
		outcome, planErr := service.Store.PlanAltMountRepair(ctx, integration.ID, item.ID, path, time.Now().UTC())
		if errors.Is(planErr, postgres.ErrConcurrentModification) {
			counts["retry_next_sweep"]++
			continue
		}
		if planErr != nil {
			return planErr
		}
		counts[outcome]++
	}
	slog.Info("AltMount repair sweep", "integration", integration.Name, "corrupted", len(items), "outcomes", counts)
	return nil
}

func (service *AltMountRepairService) listCorrupted(ctx context.Context, integration postgres.Integration) ([]altmount.HealthItem, error) {
	var config altMountConfig
	if err := json.Unmarshal(integration.Config, &config); err != nil {
		return nil, err
	}
	client, err := newAltMountClient(config, integration.SecretRef, service.ResolveSecret, service.Transport)
	if err != nil {
		return nil, err
	}
	if _, err := client.Login(ctx); err != nil {
		return nil, err
	}
	return client.ListCorruptedFiles(ctx)
}
