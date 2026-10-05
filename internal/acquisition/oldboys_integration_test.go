package acquisition

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
	"github.com/media-manager/media-manager/internal/postgres"
	"github.com/media-manager/media-manager/internal/workflows"
)

type oldBoysAcquisitionTransport func(*http.Request) (*http.Response, error)

func (f oldBoysAcquisitionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNativeOldBoysSearchUsesDanishScopeAndRejectsRotatedResult(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		t.Run(map[bool]string{false: "select", true: "rotation"}[rotate], func(t *testing.T) {
			store := openMovieSearchTestStore(t)
			ctx := context.Background()
			server, _ := newMovieSearchServer(t, func(_ url.Values) string { return emptySearchXML() })
			defer server.Close()
			existing := configureMovieSearchIndexer(t, store, server.URL, false)
			workflow := createMovieSearchWorkflow(t, store)
			vault, _ := oldboyscredentials.New(store, strings.Repeat("ab", 32))
			credentials := oldboyscredentials.Credentials{APIToken: "native-api-fixture", PassKey: "native-pid-fixture", RSSKey: "native-rid-fixture"}
			status, err := vault.Save(ctx, credentials)
			if err != nil {
				t.Fatal(err)
			}
			attempt, err := store.PrepareOldBoysCommission(ctx, *status.UpdatedAt)
			if err != nil {
				t.Fatal(err)
			}
			native, err := store.CompleteOldBoysCommission(ctx, attempt, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Pool.Exec(ctx, `UPDATE integration_instances SET enabled=false WHERE id=$1`, existing.ID.String()); err != nil {
				t.Fatal(err)
			}
			foreign, err := store.LoadSearchContext(ctx, workflow.ID)
			if err != nil || len(foreign.Indexers) != 0 {
				t.Fatal("unknown-language movie was sent to Danish-only provider", err)
			}
			if _, err := store.RefreshMediaMetadata(ctx, workflow.MediaID, &catalog.MediaMetadata{OriginalTitle: "Julemanden", OriginalLanguage: "da"}, nil, "test", time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			search, err := store.LoadSearchContext(ctx, workflow.ID)
			if err != nil || len(search.Indexers) != 1 || search.Indexers[0].ID != native.ID {
				t.Fatal("Danish native provider not planned", err)
			}
			calls := 0
			service := SearchService{Store: store, Scheduler: indexers.NewScheduler(1, store), OldBoysCredentials: vault, Transport: oldBoysAcquisitionTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "oldboys.pw" || r.URL.Query().Get("name") != "Julemanden 2019 DANiSH" || r.URL.Query().Get("categories[]") != "1" {
					t.Fatal("native workflow query incorrect")
				}
				if rotate {
					if _, err := vault.Save(ctx, credentials); err != nil {
						t.Fatal(err)
					}
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"123","attributes":{"name":"Julemanden.2019.DANiSH.1080p.WEB-DL","category_id":1,"size":null,"imdb_id":null}}]}`))}, nil
			})}
			err = service.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: search.Workflow.Revision})
			if rotate {
				if !errors.Is(err, postgres.ErrConcurrentModification) {
					t.Fatal("credential rotation did not stale persistence", err)
				}
				var count int
				if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM release_candidates WHERE workflow_id=$1`, workflow.ID.String()).Scan(&count); err != nil || count != 0 {
					t.Fatal("stale result persisted")
				}
				return
			}
			if err != nil || calls != 1 {
				t.Fatal("native workflow search failed", calls, err)
			}
			after, err := store.LoadSearchContext(ctx, workflow.ID)
			if err != nil || after.Workflow.State != workflows.StateCandidateSelected {
				t.Fatal("eligible native movie not selected", err)
			}
			retrieval, err := store.LoadRetrievalContext(ctx, workflow.ID, *after.Workflow.SelectedRelease)
			if err != nil || retrieval.NativeID != "123" || retrieval.DownloadLocatorRef != "native:123" || retrieval.Indexer.ID != native.ID {
				t.Fatalf("native retrieval identity/locator changed: native=%q locator=%q indexer=%v want=%v err=%v", retrieval.NativeID, retrieval.DownloadLocatorRef, retrieval.Indexer.ID, native.ID, err)
			}
			time.Sleep(5 * time.Second)
			stop := errors.New("fixture stops before AltMount authentication")
			grabs := 0
			retrieveService := RetrievalService{Store: store, Scheduler: indexers.NewScheduler(1, store), OldBoysCredentials: vault, ResolveSecret: func(reference string) (string, error) {
				if reference == "sealed:oldboys_credentials" {
					t.Fatal("native credentials were routed through environment secrets")
				}
				return "", stop
			}, Transport: oldBoysAcquisitionTransport(func(r *http.Request) (*http.Response, error) {
				grabs++
				if r.URL.Host != "oldboys.pw" || r.URL.Path != "/nzbs/download/123.native-pid-fixture" || r.Header.Get("Authorization") != "Bearer native-api-fixture" {
					t.Fatal("native workflow download contract changed")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<nzb><file subject="video.mkv"><segments><segment bytes="1" number="1">fixture@test</segment></segments></file></nzb>`))}, nil
			})}
			payload := []byte(fmt.Sprintf(`{"workflow_id":%q,"candidate_id":%q}`, workflow.ID.String(), after.Workflow.SelectedRelease.String()))
			if err := retrieveService.Handle(ctx, postgres.OutboxEvent{AggregateID: workflow.ID, AggregateRevision: after.Workflow.Revision, Payload: payload}); !errors.Is(err, stop) || grabs != 1 {
				t.Fatal("guarded native retrieval failed", grabs, err)
			}

		})
	}
}
