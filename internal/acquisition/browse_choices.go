package acquisition

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/releases"
)

// browseChoiceStoreTimeout bounds the durable copy of a stream list so a slow
// database cannot hold up the list itself.
const browseChoiceStoreTimeout = 5 * time.Second

// persistChoices stores a stream list's choices so they survive a restart.
// The in-memory copy stays authoritative for this process: if the database
// write fails the list still works until the next restart, exactly as before.
func (s *BrowseService) persistChoices(ctx context.Context, choices map[string]browseChoice) {
	if len(choices) == 0 || s.Search == nil || s.Search.Store == nil {
		return
	}
	now := time.Now().UTC()
	rows := make([]postgres.StreamingChoice, 0, len(choices))
	for token, choice := range choices {
		id, err := uuid.Parse(token)
		if err != nil {
			continue
		}
		request, err := json.Marshal(choice.input)
		if err != nil {
			continue
		}
		rows = append(rows, postgres.StreamingChoice{
			Token:                  id,
			Request:                request,
			IntegrationID:          choice.integration.ID,
			IntegrationFingerprint: postgres.IntegrationFingerprint(choice.integration),
			NativeGUID:             choice.observation.NativeGUID,
			Title:                  choice.observation.Title,
			LocatorRef:             choice.locator,
			CreatedAt:              now,
			ExpiresAt:              choice.expires,
		})
	}
	store, cancel := context.WithTimeout(context.WithoutCancel(ctx), browseChoiceStoreTimeout)
	defer cancel()
	if err := s.Search.Store.SaveStreamingChoices(store, rows); err != nil {
		slog.Warn("stream list choices were not stored; they expire on restart", "choices", len(rows), "error", err)
	}
}

// storedChoice loads a choice made before this process started. It is usable
// only while its indexer is enabled with the exact configuration it was found
// with; otherwise the viewer must refresh the stream list, as for an in-memory
// choice whose indexer changed.
func (s *BrowseService) storedChoice(ctx context.Context, token string) (browseChoice, bool, error) {
	id, err := uuid.Parse(token)
	if err != nil || s.Search == nil || s.Search.Store == nil {
		return browseChoice{}, false, nil
	}
	stored, found, err := s.Search.Store.StreamingChoiceByToken(ctx, id, time.Now())
	if err != nil || !found {
		return browseChoice{}, false, err
	}
	var input BrowseInput
	if err := json.Unmarshal(stored.Request, &input); err != nil || !input.Valid() {
		return browseChoice{}, false, nil
	}
	integrations, err := s.Search.Store.ListIntegrations(ctx)
	if err != nil {
		return browseChoice{}, false, err
	}
	for _, integration := range integrations {
		if integration.ID != stored.IntegrationID || !integration.Enabled || postgres.IntegrationFingerprint(integration) != stored.IntegrationFingerprint {
			continue
		}
		choice := browseChoice{
			input:       input,
			integration: integration,
			observation: releases.Observation{IndexerID: integration.ID, NativeGUID: stored.NativeGUID, Title: stored.Title},
			locator:     stored.LocatorRef,
			expires:     stored.ExpiresAt,
		}
		s.mu.Lock()
		if s.choices == nil {
			s.choices = map[string]browseChoice{}
			s.cache = map[BrowseInput]browseCache{}
		}
		if len(s.choices) < maxBrowseChoices {
			s.choices[token] = choice
		}
		s.mu.Unlock()
		return choice, true, nil
	}
	return browseChoice{}, false, nil
}
