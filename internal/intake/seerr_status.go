package intake

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"uuid"

	seerradapter "github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
)

var ErrSeerrStatusIdentityMismatch = errors.New("Seerr status target identity does not match Media Manager")

type seerrStatusClient interface {
	Request(context.Context, int64) (seerradapter.Request, error)
	SetMediaStatus(context.Context, int64, seerradapter.MediaStatus, []int16) (seerradapter.Media, error)
}

type seerrStatusStore interface {
	LoadSeerrStatusSyncContext(context.Context, string, uuid.UUID) (postgres.SeerrStatusSyncContext, error)
}

type SeerrStatusService struct {
	Client     seerrStatusClient
	Store      seerrStatusStore
	InstanceID string
}

func (service *SeerrStatusService) Handle(ctx context.Context, event postgres.OutboxEvent) error {
	var payload struct {
		WorkflowID string `json:"workflow_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	workflowID, err := uuid.Parse(payload.WorkflowID)
	if err != nil {
		return err
	}
	statusContext, err := service.Store.LoadSeerrStatusSyncContext(ctx, service.InstanceID, workflowID)
	if err != nil {
		return err
	}
	if len(statusContext.RequestIDs) == 0 {
		return nil
	}

	var current seerradapter.Request
	found := false
	for _, requestID := range statusContext.RequestIDs {
		parsed, parseErr := strconv.ParseInt(requestID, 10, 64)
		if parseErr != nil || parsed <= 0 {
			return errors.New("invalid persisted Seerr request ID")
		}
		current, err = service.Client.Request(ctx, parsed)
		if errors.Is(err, seerradapter.ErrRequestNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if current.Status != 2 && current.Status != 5 {
			continue
		}
		found = true
		break
	}
	if !found {
		return seerradapter.ErrRequestNotFound
	}
	if current.Media.ID <= 0 || current.Is4K || !seerrStatusIdentityMatches(statusContext, current) {
		return ErrSeerrStatusIdentityMismatch
	}

	if statusContext.Kind == catalog.KindSeries && len(statusContext.AvailableSeasons) == 0 {
		// Episode workflows confirm one at a time, so a requested season can
		// hold Plex-confirmed episodes before it is complete. Seerr has no
		// per-season partial status here; report the media as partially
		// available. Without any confirmed requested content there is nothing
		// truthful to report, and an error would only retry the same answer.
		if !statusContext.PartialContent {
			return nil
		}
		_, err := service.Client.SetMediaStatus(ctx, current.Media.ID, seerradapter.MediaStatusPartial, nil)
		return err
	}
	if _, err := service.Client.SetMediaStatus(ctx, current.Media.ID, seerradapter.MediaStatusAvailable, statusContext.AvailableSeasons); err != nil {
		return err
	}
	if statusContext.Kind == catalog.KindSeries && !statusContext.AllAvailable {
		if _, err := service.Client.SetMediaStatus(ctx, current.Media.ID, seerradapter.MediaStatusPartial, nil); err != nil {
			return err
		}
	}
	return nil
}

func seerrStatusIdentityMatches(statusContext postgres.SeerrStatusSyncContext, request seerradapter.Request) bool {
	wantKind := "movie"
	if statusContext.Kind == catalog.KindSeries {
		wantKind = "tv"
	}
	if request.Media.MediaType != wantKind || (request.Type != "" && request.Type != wantKind) {
		return false
	}
	expected := make(map[string]map[string]struct{})
	for _, identity := range statusContext.Identities {
		if identity.State != catalog.IdentityVerified {
			continue
		}
		namespace := strings.ToLower(strings.TrimSpace(identity.Namespace))
		value := strings.TrimSpace(identity.Value)
		if value == "" {
			continue
		}
		if expected[namespace] == nil {
			expected[namespace] = make(map[string]struct{})
		}
		expected[namespace][value] = struct{}{}
	}
	matched := false
	for _, identity := range request.Identities() {
		values, known := expected[identity.Namespace]
		if !known {
			continue
		}
		if _, exact := values[identity.Value]; !exact {
			return false
		}
		matched = true
	}
	return matched
}
