package gmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/media-manager/media-manager/internal/catalog"
)

func verified(namespace, value string) catalog.ExternalIdentity {
	return catalog.ExternalIdentity{Namespace: namespace, Value: value, State: catalog.IdentityVerified}
}

func TestLookupRequiresExactHardIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/search":
			if request.URL.Query().Get("q") != "The Paper" || request.URL.Query().Get("limit") != "40" {
				t.Fatalf("unexpected bounded search: %s", request.URL.RequestURI())
			}
			_, _ = writer.Write([]byte(`{"items":[{"id":"title_wrong000","name":"The Paper"},{"id":"title_exact000","name":"The Paper"}]}`))
		case "/api/v1/titles/title_wrong000":
			_, _ = writer.Write([]byte(`{"title":{"name":"The Paper"},"external_ids":[{"source":"tmdb","id":"999"}]}`))
		case "/api/v1/titles/title_exact000":
			_, _ = writer.Write([]byte(`{"title":{"name":"The Paper","overview":"Real overview"},"external_ids":[{"source":"tmdb","id":"253941"},{"source":"tvdb","id":"449872"},{"source":"simkl","id":"2459135"}],"events":[{"id":"event_one","type":"series_premiere","date":"2026-09-01","season_number":-1,"episode_number":-1,"date_conflict":0,"confidence":0.8}]}`))
		default:
			t.Fatalf("unexpected request: %s", request.URL.Path)
		}
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	detail, matched, err := client.Lookup(context.Background(), "The Paper", []catalog.ExternalIdentity{verified("tmdb", "253941")})
	if err != nil || !matched || detail.Title.Overview != "Real overview" || len(detail.Events) != 1 || detail.Events[0].SeasonNumber != -1 || detail.Events[0].DateConflict != 0 {
		t.Fatalf("detail=%+v matched=%v err=%v", detail, matched, err)
	}

	_, matched, err = client.Lookup(context.Background(), "The Paper", []catalog.ExternalIdentity{verified("tmdb", "111")})
	if err != nil || matched {
		t.Fatalf("title-only match was accepted: matched=%v err=%v", matched, err)
	}
}

func TestLookupRejectsConflictingProviderIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/v1/search" {
			_, _ = writer.Write([]byte(`{"items":[{"id":"title_conflict0","name":"Series"}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{"title":{"name":"Series"},"external_ids":[{"source":"tmdb","id":"42"},{"source":"tvdb","id":"999"}]}`))
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, AllowHTTP: true}, server.Client().Transport)
	_, matched, err := client.Lookup(context.Background(), "Series", []catalog.ExternalIdentity{verified("tmdb", "42"), verified("tvdb", "77")})
	if err != nil || matched {
		t.Fatalf("conflicting identity was accepted: matched=%v err=%v", matched, err)
	}
}

func TestLookupBoundsDetailsAndNeverFollowsRedirects(t *testing.T) {
	var detailRequests atomic.Int32
	items := make([]string, 13)
	for index := range items {
		items[index] = fmt.Sprintf(`{"id":"title_%08d","name":"Series"}`, index)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/v1/search" {
			_, _ = writer.Write([]byte(`{"items":[` + strings.Join(items, ",") + `]}`))
			return
		}
		detailRequests.Add(1)
		_, _ = writer.Write([]byte(`{"title":{"name":"Series"},"external_ids":[{"source":"tmdb","id":"999"}]}`))
	}))
	defer server.Close()
	client, _ := New(Config{BaseURL: server.URL, AllowHTTP: true}, server.Client().Transport)
	_, matched, err := client.Lookup(context.Background(), "Series", []catalog.ExternalIdentity{verified("tmdb", "42")})
	if err != nil || matched || detailRequests.Load() != maximumCandidates {
		t.Fatalf("matched=%v requests=%d err=%v", matched, detailRequests.Load(), err)
	}

	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("redirect was followed") }))
	defer redirectTarget.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, redirectTarget.URL, http.StatusFound)
	}))
	defer redirector.Close()
	redirectClient, _ := New(Config{BaseURL: redirector.URL, AllowHTTP: true}, redirector.Client().Transport)
	if _, _, err := redirectClient.Lookup(context.Background(), "Series", []catalog.ExternalIdentity{verified("tmdb", "42")}); err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("redirect response was not rejected: %v", err)
	}
}

func TestNewRequiresExplicitHTTPOptIn(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://gmd:8080"}, nil); err == nil {
		t.Fatal("plain HTTP was accepted without explicit opt-in")
	}
	if _, err := New(Config{BaseURL: "https://user:secret@example.test"}, nil); err == nil {
		t.Fatal("URL userinfo was accepted")
	}
}
