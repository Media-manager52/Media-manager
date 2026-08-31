package acquisition

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/altmount"
	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

const maxReleaseRetrievalAttempts = 2

type RetrievalService struct {
	Store         *postgres.Store
	Scheduler     *indexers.Scheduler
	ResolveSecret SecretResolver
	Transport     http.RoundTripper
}

type altMountConfig struct {
	BaseURL           string `json:"base_url"`
	Authentication    string `json:"authentication"`
	AllowHTTP         bool   `json:"allow_http"`
	Category          string `json:"category"`
	RequestTimeoutMS  int64  `json:"request_timeout_ms"`
	ReportedRoot      string `json:"reported_root"`
	LocalReadOnlyRoot string `json:"local_read_only_root"`
}
type altMountSecret struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func newAltMountClient(config altMountConfig, secretRef string, resolveSecret SecretResolver, transport http.RoundTripper) (*altmount.HTTPClient, error) {
	clientConfig := altmount.Config{BaseURL: config.BaseURL, AllowHTTP: config.AllowHTTP, RequestTimeout: time.Duration(config.RequestTimeoutMS) * time.Millisecond}
	switch strings.ToLower(strings.TrimSpace(config.Authentication)) {
	case "anonymous_admin":
		clientConfig.AllowAnonymousAdmin = true
	case "", "session":
		if resolveSecret == nil {
			return nil, errors.New("AltMount secret resolver is unavailable")
		}
		secretValue, err := resolveSecret(secretRef)
		if err != nil {
			return nil, err
		}
		var secret altMountSecret
		if err := json.Unmarshal([]byte(secretValue), &secret); err != nil {
			return nil, err
		}
		clientConfig.Username, clientConfig.Password = secret.Username, secret.Password
	default:
		return nil, errors.New("unsupported AltMount authentication mode")
	}
	return altmount.New(clientConfig, transport)
}

func (service *RetrievalService) Handle(ctx context.Context, event postgres.OutboxEvent) error {
	var payload struct {
		WorkflowID  string `json:"workflow_id"`
		CandidateID string `json:"candidate_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	workflowID, err := uuid.Parse(payload.WorkflowID)
	if err != nil {
		return err
	}
	candidateID, err := uuid.Parse(payload.CandidateID)
	if err != nil {
		return err
	}
	retrieval, err := service.Store.LoadRetrievalContext(ctx, workflowID, candidateID)
	if err != nil {
		return err
	}
	if retrieval.Workflow.State == workflows.StateSubmitting && retrieval.Workflow.GrabAttemptID != nil {
		return service.Store.MarkAltMountUnknown(ctx, workflowID, *retrieval.Workflow.GrabAttemptID, time.Now().UTC())
	}
	if event.AggregateRevision != retrieval.Workflow.Revision || retrieval.Workflow.State != workflows.StateCandidateSelected {
		return nil
	}
	if retrieval.DownloadClient.Kind != "altmount" {
		return errors.New("selected download client adapter is not implemented")
	}
	var indexerConfiguration indexerConfig
	if err := json.Unmarshal(retrieval.Indexer.Config, &indexerConfiguration); err != nil {
		return err
	}
	if retrieval.Indexer.Kind != "newznab" || !retrieval.Indexer.Enabled || !indexerConfiguration.ModeVerified {
		return errors.New("selected indexer is not commissioned")
	}
	apiKey, err := service.ResolveSecret(retrieval.Indexer.SecretRef)
	if err != nil {
		return err
	}
	minimum := time.Duration(indexerConfiguration.MinimumIntervalMS) * time.Millisecond
	if minimum < 3*time.Second {
		minimum = 3 * time.Second
	}
	if err := service.Scheduler.Configure(retrieval.Indexer.ID.String(), indexers.Budget{MinimumInterval: minimum, FailureBackoff: 15 * time.Second, MaximumBackoff: 30 * time.Minute}); err != nil {
		return err
	}
	indexerClient, err := newznab.New(newznab.Config{IndexerID: retrieval.Indexer.ID.String(), BaseURL: indexerConfiguration.BaseURL, APIKey: apiKey, AllowHTTP: indexerConfiguration.AllowHTTP}, service.Scheduler, service.Transport)
	if err != nil {
		return err
	}
	retrievalID, err := newznab.RetrievalID(retrieval.NativeID, indexerConfiguration.BaseURL)
	if err != nil {
		return service.retryOrRecordRetrievalFailure(ctx, event, retrieval, err)
	}
	nzb, err := indexerClient.GetNZBPackageGuarded(ctx, retrievalID, func(guardCtx context.Context) error {
		current, loadErr := service.Store.LoadRetrievalContext(guardCtx, workflowID, candidateID)
		if errors.Is(loadErr, postgres.ErrNotFound) {
			return postgres.ErrConcurrentModification
		}
		if loadErr != nil {
			return loadErr
		}
		if current.Workflow.State != workflows.StateCandidateSelected || current.Workflow.Revision != retrieval.Workflow.Revision || current.CandidateID != retrieval.CandidateID {
			return postgres.ErrConcurrentModification
		}
		return nil
	})
	if errors.Is(err, postgres.ErrConcurrentModification) {
		return nil
	}
	if err != nil {
		return service.retryOrRecordRetrievalFailure(ctx, event, retrieval, err)
	}
	var config altMountConfig
	if err := json.Unmarshal(retrieval.DownloadClient.Config, &config); err != nil {
		return err
	}
	client, err := newAltMountClient(config, retrieval.DownloadClient.SecretRef, service.ResolveSecret, service.Transport)
	if err != nil {
		return err
	}
	if _, err := client.Login(ctx); err != nil {
		return err
	}
	intent, err := service.Store.BeginAltMountSubmission(ctx, retrieval, nzb.Identity, time.Now().UTC())
	if err != nil {
		return err
	}
	if !intent.ShouldSubmit {
		if intent.State == "submitting" {
			return service.Store.MarkAltMountUnknown(ctx, workflowID, intent.JobID, time.Now().UTC())
		}
		return nil
	}
	upload := altmount.UploadRequest{Filename: intent.Filename, Size: int64(len(nzb.Body)), Category: config.Category, Priority: altmount.PriorityNormal, Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(nzb.Body)), nil }}
	item, err := client.UploadNZB(ctx, upload)
	if err != nil {
		var ambiguous altmount.AmbiguousSubmissionError
		var apiError altmount.APIError
		if errors.As(err, &ambiguous) || errors.As(err, &apiError) && (apiError.StatusCode == http.StatusRequestTimeout || apiError.StatusCode == http.StatusTooManyRequests || apiError.StatusCode >= 500) {
			return service.Store.MarkAltMountUnknown(ctx, workflowID, intent.JobID, time.Now().UTC())
		}
		return service.Store.MarkAltMountFailed(ctx, workflowID, intent.JobID, time.Now().UTC())
	}
	return service.Store.MarkAltMountSubmitted(ctx, workflowID, intent.JobID, int64(item.ID), time.Now().UTC())
}

func (service *RetrievalService) retryOrRecordRetrievalFailure(ctx context.Context, event postgres.OutboxEvent, retrieval postgres.RetrievalContext, cause error) error {
	var deferred interface{ RetryAt() time.Time }
	if errors.As(cause, &deferred) {
		if terminalRetrievalAccessFailure(event.LastError) {
			detail := "selected release NZB access was rejected after one bounded request (" + event.LastError + ")"
			return service.Store.RecordReleaseRetrievalFailure(ctx, retrieval, detail, time.Now().UTC())
		}
		return cause
	}
	if event.Attempts < maxReleaseRetrievalAttempts {
		return cause
	}
	detail := "selected release NZB could not be retrieved after two bounded requests"
	var coded interface{ SafeCode() string }
	if errors.As(cause, &coded) {
		detail += " (" + coded.SafeCode() + ")"
	}
	if err := service.Store.RecordReleaseRetrievalFailure(ctx, retrieval, detail, time.Now().UTC()); err != nil {
		return errors.Join(cause, err)
	}
	return nil
}

func terminalRetrievalAccessFailure(safeCode string) bool {
	return safeCode == "newznab_http_401" || safeCode == "newznab_http_403"
}
