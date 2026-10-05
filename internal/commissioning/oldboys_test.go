package commissioning

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
)

type oldBoysTestTransport func(*http.Request) (*http.Response, error)

func (f oldBoysTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOldBoysNativeCommissionValidatesFivePacedRequests(t *testing.T) {
	store, ctx := draftServiceStore(t)
	vault, err := oldboyscredentials.New(store, strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Save(ctx, oldboyscredentials.Credentials{APIToken: "commission-api-fixture", PassKey: "commission-pid-fixture", RSSKey: "commission-rid-fixture"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	service := OldBoysService{Store: store, Scheduler: indexers.NewScheduler(1, store), Credentials: vault, Transport: oldBoysTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body := `{"data":[]}`
		if r.URL.Path == "/api/nzbs/filter" {
			if strings.HasPrefix(r.URL.Query().Get("name"), "Operation X") {
				body = `{"data":[{"id":"123","attributes":{"name":"Operation.X.S2026E01.DANiSH.1080p.WEB-DL","category_id":2}}]}`
			}
			if strings.HasPrefix(r.URL.Query().Get("name"), "Klovn") {
				body = `{"data":[{"id":"124","attributes":{"name":"Klovn.The.Movie.2010.DANiSH.1080p.BluRay","category_id":1}}]}`
			}
		} else if r.URL.Path == "/nzbs/download/123.commission-pid-fixture" {
			body = `<nzb><file subject="video.mkv"><segments><segment bytes="1" number="1">test@fixture</segment></segments></file></nzb>`
		} else {
			t.Fatal("unexpected provider path")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	integration, err := service.Commission(ctx)
	if err != nil || !integration.Enabled || calls != 5 {
		t.Fatal("native commission failed", calls, err)
	}
	var jobs int
	if err := store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM altmount_jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("commissioning created acquisition work")
	}
}
func TestNativeCommissionRejectsFilteredNonemptyNegativeControl(t *testing.T) {
	store, ctx := draftServiceStore(t)
	vault, _ := oldboyscredentials.New(store, strings.Repeat("ab", 32))
	_, err := vault.Save(ctx, oldboyscredentials.Credentials{APIToken: "fixture-api", PassKey: "fixture-pid", RSSKey: "fixture-rid"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	service := OldBoysService{Store: store, Scheduler: indexers.NewScheduler(1, store), Credentials: vault, Transport: oldBoysTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body := `{"data":[{"id":"123","attributes":{"name":"Operation.X.S2026E01.DANiSH.1080p.WEB","category_id":2}}]}`
		if calls == 2 {
			body = `{"data":[{"id":"1","attributes":{"name":"foreign malformed title","category_id":1}}]}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if _, err := service.Commission(ctx); err == nil || calls != 2 {
		t.Fatal("false negative commissioned", calls, err)
	}
	status, err := vault.Status(ctx)
	if err != nil || status.Enabled {
		t.Fatal("failed proof enabled provider")
	}
}
