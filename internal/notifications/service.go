package notifications

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/media-manager/media-manager/internal/postgres"
)

type SecretResolver func(string) (string, error)

type Service struct {
	Store         *postgres.Store
	ResolveSecret SecretResolver
	Transport     http.RoundTripper
}

func (service *Service) Handle(ctx context.Context, event postgres.OutboxEvent) error {
	result, err := service.Store.NotificationDeliveryContext(ctx, event.AggregateID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if result.Delivery.State == "delivered" {
		return nil
	}
	if !result.Enabled {
		return service.Store.MarkNotificationDelivery(ctx, result.Delivery.ID, false, 0, "destination disabled", time.Now().UTC())
	}
	status, detail, err := service.deliver(ctx, result)
	if err != nil {
		if markErr := service.Store.MarkNotificationDelivery(ctx, result.Delivery.ID, false, status, detail, time.Now().UTC()); markErr != nil {
			return markErr
		}
		return err
	}
	return service.Store.MarkNotificationDelivery(ctx, result.Delivery.ID, true, status, "", time.Now().UTC())
}

func (service *Service) deliver(ctx context.Context, result postgres.NotificationDeliveryContext) (int, string, error) {
	parsed, err := url.Parse(result.Endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !(result.AllowHTTP && parsed.Scheme == "http")) {
		return 0, "invalid destination configuration", errors.New("invalid notification destination")
	}
	secret := ""
	if result.SecretRef != "" {
		if service.ResolveSecret == nil {
			return 0, "secret unavailable", errors.New("notification secret resolver is unavailable")
		}
		secret, err = service.ResolveSecret(result.SecretRef)
		if err != nil {
			return 0, "secret unavailable", err
		}
	}
	transport := service.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("notification redirects are disabled") }}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, result.Endpoint, bytes.NewReader(result.Delivery.Payload))
	if err != nil {
		return 0, "request construction failed", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Media-Manager/notifications")
	if secret != "" {
		request.Header.Set("Authorization", "Bearer "+secret)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "transport failure", err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, "destination returned non-success status", fmt.Errorf("notification destination returned HTTP %d", response.StatusCode)
	}
	return response.StatusCode, "", nil
}
