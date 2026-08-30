package notifications

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestDeliverUsesExactRedactedPayloadAndEnvironmentSecret(t *testing.T) {
	payload := []byte(`{"event_id":"event-1","action":"workflow.created","aggregate_type":"workflow","aggregate_id":"workflow-1"}`)
	service := &Service{
		ResolveSecret: func(reference string) (string, error) {
			if reference != "OWNER_NOTIFICATION_TOKEN" {
				t.Fatalf("secret reference=%q", reference)
			}
			return "resolved-test-token", nil
		},
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != string(payload) {
				t.Fatalf("body=%s", body)
			}
			if request.Header.Get("Authorization") != "Bearer resolved-test-token" {
				t.Fatalf("authorization header was not resolved at delivery time")
			}
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}),
	}
	status, detail, err := service.deliver(context.Background(), postgres.NotificationDeliveryContext{
		Delivery:  postgres.NotificationDelivery{Payload: payload},
		Endpoint:  "https://notifications.invalid/hook-token",
		SecretRef: "OWNER_NOTIFICATION_TOKEN",
		Enabled:   true,
	})
	if err != nil || status != http.StatusNoContent || detail != "" {
		t.Fatalf("status=%d detail=%q err=%v", status, detail, err)
	}
}

func TestDeliverRejectsUnapprovedPlainHTTP(t *testing.T) {
	called := false
	service := &Service{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}
	status, detail, err := service.deliver(context.Background(), postgres.NotificationDeliveryContext{
		Delivery: postgres.NotificationDelivery{Payload: []byte(`{}`)},
		Endpoint: "http://notifications.invalid/hook",
		Enabled:  true,
	})
	if err == nil || status != 0 || detail != "invalid destination configuration" || called {
		t.Fatalf("status=%d detail=%q err=%v called=%t", status, detail, err, called)
	}
}
