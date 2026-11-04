package intake

import (
	"context"
	"errors"
	"testing"
	"uuid"

	seerradapter "github.com/media-manager/media-manager/internal/adapters/seerr"
	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/postgres"
)

type fakeSeerrStatusClient struct {
	request seerradapter.Request
	err     error
	calls   []seerrStatusCall
}

type seerrStatusCall struct {
	id      int64
	status  seerradapter.MediaStatus
	seasons []int16
	is4k    bool
}

func (client *fakeSeerrStatusClient) Request(context.Context, int64) (seerradapter.Request, error) {
	return client.request, client.err
}

func (client *fakeSeerrStatusClient) SetMediaStatus(_ context.Context, id int64, status seerradapter.MediaStatus, seasons []int16, is4k bool) (seerradapter.Media, error) {
	client.calls = append(client.calls, seerrStatusCall{id: id, status: status, seasons: append([]int16(nil), seasons...), is4k: is4k})
	return seerradapter.Media{ID: id}, nil
}

type fakeSeerrStatusStore struct {
	value postgres.SeerrStatusSyncContext
	err   error
}

func (store fakeSeerrStatusStore) LoadSeerrStatusSyncContext(context.Context, string, uuid.UUID) (postgres.SeerrStatusSyncContext, error) {
	return store.value, store.err
}

func TestSeerrStatusServicePublishesConfirmedSeasonsThenPartial(t *testing.T) {
	tmdbID := int64(123)
	client := &fakeSeerrStatusClient{request: seerradapter.Request{ID: 9, Status: 2, Type: "tv", Media: seerradapter.Media{ID: 77, TMDBID: &tmdbID, MediaType: "tv"}}}
	store := fakeSeerrStatusStore{value: postgres.SeerrStatusSyncContext{
		Kind: catalog.KindSeries, RequestIDs: []string{"9"}, AvailableSeasons: []int16{2, 3}, AllAvailable: false,
		Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "123", State: catalog.IdentityVerified}},
	}}
	service := &SeerrStatusService{Client: client, Store: store, InstanceID: uuid.NewV7().String()}
	payload := []byte(`{"workflow_id":"` + uuid.NewV7().String() + `"}`)
	if err := service.Handle(context.Background(), postgres.OutboxEvent{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 2 || client.calls[0].status != seerradapter.MediaStatusAvailable || client.calls[1].status != seerradapter.MediaStatusPartial || len(client.calls[0].seasons) != 2 {
		t.Fatalf("unexpected status calls: %+v", client.calls)
	}
}

func TestSeerrStatusServiceMarksAllAvailableOnce(t *testing.T) {
	tmdbID := int64(456)
	client := &fakeSeerrStatusClient{request: seerradapter.Request{ID: 10, Status: 2, Type: "movie", Media: seerradapter.Media{ID: 78, TMDBID: &tmdbID, MediaType: "movie"}}}
	store := fakeSeerrStatusStore{value: postgres.SeerrStatusSyncContext{
		Kind: catalog.KindMovie, RequestIDs: []string{"10"}, AllAvailable: true,
		Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "456", State: catalog.IdentityVerified}},
	}}
	service := &SeerrStatusService{Client: client, Store: store, InstanceID: uuid.NewV7().String()}
	payload := []byte(`{"workflow_id":"` + uuid.NewV7().String() + `"}`)
	if err := service.Handle(context.Background(), postgres.OutboxEvent{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 1 || client.calls[0].status != seerradapter.MediaStatusAvailable {
		t.Fatalf("unexpected status calls: %+v", client.calls)
	}
}

func TestSeerrStatusServiceReportsPartialForConfirmedEpisodesOnly(t *testing.T) {
	tmdbID := int64(321)
	client := &fakeSeerrStatusClient{request: seerradapter.Request{ID: 12, Status: 2, Type: "tv", Media: seerradapter.Media{ID: 80, TMDBID: &tmdbID, MediaType: "tv"}}}
	store := fakeSeerrStatusStore{value: postgres.SeerrStatusSyncContext{
		Kind: catalog.KindSeries, RequestIDs: []string{"12"}, PartialContent: true,
		Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "321", State: catalog.IdentityVerified}},
	}}
	service := &SeerrStatusService{Client: client, Store: store, InstanceID: uuid.NewV7().String()}
	payload := []byte(`{"workflow_id":"` + uuid.NewV7().String() + `"}`)
	if err := service.Handle(context.Background(), postgres.OutboxEvent{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 1 || client.calls[0].status != seerradapter.MediaStatusPartial || len(client.calls[0].seasons) != 0 {
		t.Fatalf("unexpected status calls: %+v", client.calls)
	}
}

func TestSeerrStatusServiceReportsNothingWithoutConfirmedRequestedContent(t *testing.T) {
	tmdbID := int64(654)
	client := &fakeSeerrStatusClient{request: seerradapter.Request{ID: 13, Status: 2, Type: "tv", Media: seerradapter.Media{ID: 81, TMDBID: &tmdbID, MediaType: "tv"}}}
	store := fakeSeerrStatusStore{value: postgres.SeerrStatusSyncContext{
		Kind: catalog.KindSeries, RequestIDs: []string{"13"},
		Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "654", State: catalog.IdentityVerified}},
	}}
	service := &SeerrStatusService{Client: client, Store: store, InstanceID: uuid.NewV7().String()}
	payload := []byte(`{"workflow_id":"` + uuid.NewV7().String() + `"}`)
	if err := service.Handle(context.Background(), postgres.OutboxEvent{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 0 {
		t.Fatalf("status reached Seerr without confirmed requested content: %+v", client.calls)
	}
}

func TestSeerrStatusServiceStopsBeforeMutationOnIdentityMismatch(t *testing.T) {
	tmdbID := int64(999)
	client := &fakeSeerrStatusClient{request: seerradapter.Request{ID: 11, Status: 2, Type: "tv", Media: seerradapter.Media{ID: 79, TMDBID: &tmdbID, MediaType: "tv"}}}
	store := fakeSeerrStatusStore{value: postgres.SeerrStatusSyncContext{
		Kind: catalog.KindSeries, RequestIDs: []string{"11"}, AvailableSeasons: []int16{1}, AllAvailable: true,
		Identities: []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "123", State: catalog.IdentityVerified}},
	}}
	service := &SeerrStatusService{Client: client, Store: store, InstanceID: uuid.NewV7().String()}
	payload := []byte(`{"workflow_id":"` + uuid.NewV7().String() + `"}`)
	if err := service.Handle(context.Background(), postgres.OutboxEvent{Payload: payload}); !errors.Is(err, ErrSeerrStatusIdentityMismatch) {
		t.Fatalf("identity mismatch error=%v", err)
	}
	if len(client.calls) != 0 {
		t.Fatalf("identity mismatch reached status endpoint: %+v", client.calls)
	}
}

// TestSeerrStatusServiceAnswersEachEditionSeparately proves a confirmed 4K
// workflow marks a 4K request available through Seerr's 4K status, and that a
// standard request is never answered from a 4K workflow (or the reverse).
func TestSeerrStatusServiceAnswersEachEditionSeparately(t *testing.T) {
	tmdbID := int64(654)
	identities := []catalog.ExternalIdentity{{Namespace: "tmdb", Value: "654", State: catalog.IdentityVerified}}
	payload := []byte(`{"workflow_id":"` + uuid.NewV7().String() + `"}`)
	fourK := &fakeSeerrStatusClient{request: seerradapter.Request{ID: 20, Status: 2, Type: "movie", Is4K: true, Media: seerradapter.Media{ID: 90, TMDBID: &tmdbID, MediaType: "movie"}}}
	store := fakeSeerrStatusStore{value: postgres.SeerrStatusSyncContext{Kind: catalog.KindMovie, RequestIDs: []string{"20"}, AllAvailable: true, Identities: identities, Edition: postgres.Edition4K}}
	if err := (&SeerrStatusService{Client: fourK, Store: store, InstanceID: uuid.NewV7().String()}).Handle(context.Background(), postgres.OutboxEvent{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if len(fourK.calls) != 1 || fourK.calls[0].status != seerradapter.MediaStatusAvailable || !fourK.calls[0].is4k {
		t.Fatalf("4K status calls: %+v, want one 4K available", fourK.calls)
	}
	standard := &fakeSeerrStatusClient{request: seerradapter.Request{ID: 21, Status: 2, Type: "movie", Media: seerradapter.Media{ID: 90, TMDBID: &tmdbID, MediaType: "movie"}}}
	store.value.RequestIDs = []string{"21"}
	if err := (&SeerrStatusService{Client: standard, Store: store, InstanceID: uuid.NewV7().String()}).Handle(context.Background(), postgres.OutboxEvent{Payload: payload}); err == nil || len(standard.calls) != 0 {
		t.Fatalf("standard request answered from a 4K workflow: err=%v calls=%+v", err, standard.calls)
	}
	store.value.Edition = postgres.EditionStandard
	if err := (&SeerrStatusService{Client: fourK, Store: store, InstanceID: uuid.NewV7().String()}).Handle(context.Background(), postgres.OutboxEvent{Payload: payload}); err == nil || len(fourK.calls) != 1 {
		t.Fatalf("4K request answered from a standard workflow: err=%v calls=%+v", err, fourK.calls)
	}
}
