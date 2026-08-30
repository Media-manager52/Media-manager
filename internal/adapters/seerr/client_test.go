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
