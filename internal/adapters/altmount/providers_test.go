package altmount

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type providerRequest struct {
	Method, Path string
	Body         map[string]any
}

func fakeProviderAltMount(t *testing.T, handle func(writer http.ResponseWriter, request providerRequest)) (*HTTPClient, *[]providerRequest) {
	t.Helper()
	requests := []providerRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/auth/login" {
			http.SetCookie(writer, &http.Cookie{Name: "JWT", Value: "session", Path: "/"})
			_, _ = io.WriteString(writer, `{"success":true,"data":{"user":{"id":"u1","is_admin":true}}}`)
			return
		}
		raw, _ := io.ReadAll(request.Body)
		recorded := providerRequest{Method: request.Method, Path: request.URL.Path}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &recorded.Body); err != nil {
				t.Errorf("request body is not JSON: %s", raw)
			}
		}
		requests = append(requests, recorded)
		handle(writer, recorded)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, AllowHTTP: true, Username: "mm", Password: "pw"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	return client, &requests
}

func TestListProvidersReadsOnlyTheServerList(t *testing.T) {
	client, _ := fakeProviderAltMount(t, func(writer http.ResponseWriter, request providerRequest) {
		_, _ = io.WriteString(writer, `{"success":true,"data":{"api_key":"altmount-own-key","providers":[{"id":"provider_1","host":"news.example.test","port":563,"username":"me","max_connections":20,"tls":true,"password_set":true,"enabled":true,"last_rtt_ms":41}]}}`)
	})
	providers, err := client.ListProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0].ID != "provider_1" || !providers[0].PasswordSet || providers[0].MaxConnections != 20 || providers[0].LastRTTMs != 41 {
		t.Fatalf("providers=%+v", providers)
	}
	encoded, _ := json.Marshal(providers)
	if strings.Contains(string(encoded), "altmount-own-key") {
		t.Fatal("the AltMount API key leaked into the provider list")
	}
}

func TestUpdateProviderKeepsTheStoredPasswordWhenNoneIsTyped(t *testing.T) {
	client, requests := fakeProviderAltMount(t, func(writer http.ResponseWriter, request providerRequest) {
		_, _ = io.WriteString(writer, `{"success":true,"data":{"id":"provider_2","host":"eu.example.test","port":563,"password_set":true,"enabled":false}}`)
	})
	empty, enabled := "", false
	updated, err := client.UpdateProvider(context.Background(), "provider_2", ProviderInput{Password: &empty, Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || !updated.PasswordSet {
		t.Fatalf("updated=%+v", updated)
	}
	got := (*requests)[0]
	if got.Method != http.MethodPut || got.Path != "/api/providers/provider_2" {
		t.Fatalf("request=%+v", got)
	}
	if _, sent := got.Body["password"]; sent {
		t.Fatalf("an empty password was sent and would erase the stored one: %v", got.Body)
	}
	if got.Body["enabled"] != false || len(got.Body) != 1 {
		t.Fatalf("only the changed field may be sent: %v", got.Body)
	}
}

func TestProviderRefusalKeepsAltMountsReasonAndIsSentOnce(t *testing.T) {
	client, requests := fakeProviderAltMount(t, func(writer http.ResponseWriter, request providerRequest) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, `{"success":false,"error":{"code":"VALIDATION_ERROR","message":"Valid port is required (1-65535)","details":"INVALID_PORT"}}`)
	})
	host, port, user, password := "news.example.test", 0, "me", "secret-pw"
	_, err := client.CreateProvider(context.Background(), ProviderInput{Host: &host, Port: &port, Username: &user, Password: &password})
	var apiError APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusBadRequest || apiError.Message != "Valid port is required (1-65535)" {
		t.Fatalf("err=%#v", err)
	}
	if len(*requests) != 1 {
		t.Fatalf("a refused change must be sent once, got %d requests", len(*requests))
	}
	if strings.Contains(err.Error(), "secret-pw") {
		t.Fatal("the password leaked into the error")
	}
}

func TestProviderCallLogsInAgainOnceWhenTheSessionExpired(t *testing.T) {
	calls := 0
	client, requests := fakeProviderAltMount(t, func(writer http.ResponseWriter, request providerRequest) {
		calls++
		if calls == 1 {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(writer, `{"success":true,"data":{"success":true,"rtt_ms":12}}`)
	})
	result, err := client.TestProvider(context.Background(), ProviderTest{Host: "news.example.test", Port: 563, Username: "me", Password: "pw", TLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.RTTMs != 12 || len(*requests) != 2 || (*requests)[1].Path != "/api/providers/test" {
		t.Fatalf("result=%+v requests=%+v", result, *requests)
	}
}

func TestProviderIDsAreCheckedBeforeAnyRequest(t *testing.T) {
	client, requests := fakeProviderAltMount(t, func(writer http.ResponseWriter, request providerRequest) {
		_, _ = io.WriteString(writer, `{"success":true}`)
	})
	ctx := context.Background()
	if err := client.DeleteProvider(ctx, "../config"); !errors.Is(err, ErrInvalidProviderID) {
		t.Fatalf("delete err=%v", err)
	}
	if err := client.ReorderProviders(ctx, []string{"provider_1", "a/b"}); !errors.Is(err, ErrInvalidProviderID) {
		t.Fatalf("reorder err=%v", err)
	}
	if len(*requests) != 0 {
		t.Fatalf("requests=%+v", *requests)
	}
	if err := client.ReorderProviders(ctx, []string{"provider_2", "provider_1"}); err != nil {
		t.Fatal(err)
	}
	if got := (*requests)[0]; got.Path != "/api/providers/reorder" || got.Method != http.MethodPut {
		t.Fatalf("request=%+v", got)
	}
}
