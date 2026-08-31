package seerr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseWebhookAcceptsLifecycleWakeups(t *testing.T) {
	for _, notification := range []string{"MEDIA_APPROVED", "MEDIA_DECLINED", "MEDIA_AVAILABLE"} {
		body := []byte(`{"notification_type":"` + notification + `","request_id":"42","media_type":"tv"}`)
		if _, err := ParseWebhook(body); err != nil {
			t.Fatalf("%s webhook rejected: %v", notification, err)
		}
	}
	if _, err := ParseWebhook([]byte(`{"notification_type":"MEDIA_APPROVED","request_id":"invalid","media_type":"tv"}`)); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("invalid request ID accepted: %v", err)
	}
}

func TestRequestReturnsTypedNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, APIKey: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Request(context.Background(), 42); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("not-found response was not typed: %v", err)
	}
}

func TestSearchUsesBoundedEncodedQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/search" || request.URL.Query().Get("query") != "Dune & Sand" || request.URL.Query().Get("page") != "2" {
			t.Fatalf("unexpected search URL: %s", request.URL.String())
		}
		if request.URL.RawQuery != "page=2&query=Dune%20%26%20Sand" {
			t.Fatalf("unexpected raw search query: %s", request.URL.RawQuery)
		}
		if request.Header.Get("X-Api-Key") != "secret" {
			t.Fatal("missing Seerr API key")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"page":2,"totalPages":3,"totalResults":1,"results":[{"id":42,"mediaType":"movie","title":"Dune"}]}`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, APIKey: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Search(context.Background(), "Dune & Sand", 2)
	if err != nil || len(result.Results) != 1 || result.Results[0].ID != 42 {
		t.Fatalf("search result=%+v err=%v", result, err)
	}
	if _, err := client.Search(context.Background(), "x", 1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("short query accepted: %v", err)
	}
}
