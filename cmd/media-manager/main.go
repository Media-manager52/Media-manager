package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/acquisition"
	seerradapter "github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/adoptionexec"
	"github.com/media-manager/media-manager/internal/backups"
	"github.com/media-manager/media-manager/internal/commissioning"
	"github.com/media-manager/media-manager/internal/httpapi"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/intake"
	"github.com/media-manager/media-manager/internal/mediainfo"
	"github.com/media-manager/media-manager/internal/notifications"
	"github.com/media-manager/media-manager/internal/platform"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/publication"
	"github.com/media-manager/media-manager/internal/recycle"
)

var version = "development"
var commit = "unknown"

func main() {
	if err := run(); err != nil {
		slog.Error("media manager stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "version" {
		fmt.Printf("Media Manager %s (%s)\n", version, commit)
		return nil
	}
	if command == "healthcheck" {
		endpoint := os.Getenv("HEALTHCHECK_URL")
		if endpoint == "" {
			endpoint = "http://127.0.0.1:8080/health/live"
		}
		client := &http.Client{Timeout: 3 * time.Second}
		response, requestErr := client.Get(endpoint)
		if requestErr != nil {
			return requestErr
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
		}
		return nil
	}
	databaseURL := optionalSecret("DATABASE_URL")
	if databaseURL == "" && optionalSecret("PGHOST") == "" {
		return errors.New("DATABASE_URL or libpq PG* connection variables are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	store, err := postgres.Open(ctx, databaseURL)
	cancel()
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer store.Close()
	if command == "migrate" {
		return postgres.Migrate(context.Background(), store.Pool)
	}
	if command != "serve" {
		return fmt.Errorf("unknown command %q", command)
	}
	if strings.EqualFold(os.Getenv("AUTO_MIGRATE"), "true") {
		if err := postgres.Migrate(context.Background(), store.Pool); err != nil {
			return err
		}
	}
	ownerToken, err := requiredSecret("OWNER_TOKEN")
	if err != nil {
		return err
	}
	seerrURL, seerrKey := strings.TrimSpace(os.Getenv("SEERR_BASE_URL")), optionalSecret("SEERR_API_KEY")
	seerrInstance, seerrWebhookSecret := strings.TrimSpace(os.Getenv("SEERR_INSTANCE_ID")), optionalSecret("SEERR_WEBHOOK_SECRET")
	seerrValues := []string{seerrURL, seerrKey, seerrInstance, seerrWebhookSecret}
	configuredValues := 0
	for _, value := range seerrValues {
		if value != "" {
			configuredValues++
		}
	}
	if configuredValues != 0 && configuredValues != len(seerrValues) {
		return errors.New("Seerr requires SEERR_BASE_URL, SEERR_API_KEY, SEERR_INSTANCE_ID, and SEERR_WEBHOOK_SECRET together")
	}
	seerrEnabled := configuredValues == len(seerrValues)
	if seerrEnabled && len(seerrWebhookSecret) < 32 {
		return errors.New("SEERR_WEBHOOK_SECRET must contain at least 32 bytes")
	}
	if seerrEnabled {
		instanceID, parseErr := uuid.Parse(seerrInstance)
		if parseErr != nil {
			return errors.New("SEERR_INSTANCE_ID must be a valid UUID")
		}
		integration, integrationErr := store.Integration(context.Background(), instanceID)
		if integrationErr != nil || integration.Kind != "seerr" || !integration.Enabled {
			return errors.New("SEERR_INSTANCE_ID must reference an enabled Seerr integration")
		}
	}
	var seerrClient *seerradapter.Client
	if seerrEnabled {
		seerrClient, err = seerradapter.New(seerradapter.Config{BaseURL: seerrURL, APIKey: seerrKey, AllowHTTP: strings.EqualFold(os.Getenv("SEERR_ALLOW_HTTP"), "true")}, nil)
		if err != nil {
			return err
		}
	}
	scheduler := indexers.NewScheduler(4, store)
	resolveSecret := func(reference string) (string, error) { return requiredSecret(reference) }
	backupDirectory := strings.TrimSpace(os.Getenv("BACKUP_DIRECTORY"))
	if backupDirectory == "" {
		backupDirectory = "/var/lib/media-manager/backups"
	}
	backupConfigured := optionalSecret(backups.KeyReference) != ""
	commissionService := &commissioning.NewznabService{Store: store, Scheduler: scheduler, ResolveSecret: resolveSecret}
	api, err := httpapi.New(httpapi.Config{OwnerToken: ownerToken, SeerrEnabled: seerrEnabled, SeerrWebhookSecret: seerrWebhookSecret, SeerrInstanceID: seerrInstance, Version: version, MetadataProvider: seerrClient, CommissionNewznab: commissionService.Commission, BackupConfigured: backupConfigured, BackupDirectory: backupDirectory}, store)
	if err != nil {
		return err
	}
	workerContext, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	handlers := map[string]platform.Handler{}
	searchService := &acquisition.SearchService{Store: store, Scheduler: scheduler, ResolveSecret: resolveSecret}
	handlers["workflow.search_requested"] = searchService.Handle
	retrievalService := &acquisition.RetrievalService{Store: store, Scheduler: scheduler, ResolveSecret: resolveSecret}
	handlers["release.retrieve_requested"] = retrievalService.Handle
	mediaInfoProbe := mediainfo.NewProbe()
	altMountReconciler := &acquisition.AltMountReconcileService{Store: store, ResolveSecret: resolveSecret, MediaInfoProbe: mediaInfoProbe}
	handlers["altmount.reconcile_requested"] = altMountReconciler.Handle
	publicationService := &publication.Service{Store: store}
	handlers["library.publish_requested"] = publicationService.Handle
	handlers["library.displaced_remove_requested"] = publicationService.HandleDisplacedRemoval
	plexService := &publication.PlexService{Store: store, ResolveSecret: resolveSecret}
	handlers["plex.scan_requested"] = plexService.HandleScan
	handlers["plex.confirm_requested"] = plexService.HandleConfirm
	handlers["plex.recycle_refresh_requested"] = plexService.HandleRecycleRefresh
	handlers["plex.displaced_refresh_requested"] = plexService.HandleDisplacedRefresh
	handlers["plex.adopted_restore_confirm_requested"] = plexService.HandleAdoptedRestoreConfirm
	adoptionService := &adoptionexec.Service{Store: store, MediaInfoProbe: mediaInfoProbe, PlexClientFor: func(integration postgres.Integration) (adoptionexec.PlexClient, error) {
		return plexService.ClientFor(integration)
	}}
	handlers["adoption.execute_requested"] = adoptionService.HandleExecute
	handlers["adoption.plex_confirm_requested"] = adoptionService.HandlePlexConfirm
	recycleService := &recycle.Service{Store: store}
	handlers["recycle.move_requested"] = recycleService.HandleMove
	handlers["recycle.restore_requested"] = recycleService.HandleRestore
	handlers["recycle.purge_requested"] = recycleService.HandlePurge
	handlers["workflow.transitioned"] = func(context.Context, postgres.OutboxEvent) error { return nil }
	notificationService := &notifications.Service{Store: store, ResolveSecret: resolveSecret}
	handlers["notification.deliver_requested"] = notificationService.Handle
	backupService := &backups.Service{Store: store, Directory: backupDirectory, ResolveSecret: resolveSecret}
	handlers["backup.create_requested"] = backupService.Handle
	if seerrEnabled {
		service := &intake.SeerrService{Client: seerrClient, Store: store, InstanceID: seerrInstance}
		handlers["seerr.reconcile"] = service.Handle
	}
	worker := &platform.Worker{Store: store, Owner: workerOwner(), Handlers: handlers}
	go func() {
		if workerErr := worker.Run(workerContext); workerErr != nil && !errors.Is(workerErr, context.Canceled) {
			slog.Error("outbox worker stopped", "error", workerErr)
		}
	}()
	go func() {
		_, _ = store.PlanDueRecyclePurges(workerContext, time.Now().UTC(), 100)
		_, _ = store.RequeueSeerrNeedsConfiguration(workerContext, time.Now().UTC())
		if seerrEnabled {
			_, _ = store.QueueActiveSeerrPolls(workerContext, seerrInstance, time.Now().UTC(), 25)
		}
		purgeTicker := time.NewTicker(time.Hour)
		seerrTicker := time.NewTicker(5 * time.Minute)
		defer purgeTicker.Stop()
		defer seerrTicker.Stop()
		for {
			select {
			case <-purgeTicker.C:
				_, _ = store.PlanDueRecyclePurges(workerContext, time.Now().UTC(), 100)
			case <-seerrTicker.C:
				_, _ = store.RequeueSeerrNeedsConfiguration(workerContext, time.Now().UTC())
				if seerrEnabled {
					_, _ = store.QueueActiveSeerrPolls(workerContext, seerrInstance, time.Now().UTC(), 25)
				}
			case <-workerContext.Done():
				return
			}
		}
	}()
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = "0.0.0.0:8080"
	}
	server := &http.Server{Addr: address, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		stopWorker()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("media manager API listening", "address", address, "version", version)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func requiredSecret(name string) (string, error) {
	value := optionalSecret(name)
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return strings.TrimSpace(value), nil
}

func optionalSecret(name string) string {
	if file := strings.TrimSpace(os.Getenv(name + "_FILE")); file != "" {
		body, err := os.ReadFile(file)
		if err == nil {
			return strings.TrimSpace(string(body))
		}
	}
	return strings.TrimSpace(os.Getenv(name))
}

func workerOwner() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "media-manager"
	}
	return fmt.Sprintf("%s-%d", hostname, os.Getpid())
}
