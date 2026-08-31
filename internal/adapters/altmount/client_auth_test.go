package altmount

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestAnonymousAdminLoginVerifiesLiveMode(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/auth/config":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"login_required":false}}`))
		case "/api/user":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"id":"anonymous","name":"Admin","provider":"none","is_admin":true}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := New(Config{BaseURL: server.URL, AllowHTTP: true, AllowAnonymousAdmin: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Login(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if session.User.ID != "anonymous" || !session.User.IsAdmin {
		t.Fatalf("unexpected session: %+v", session.User)
	}
	if len(requests) != 2 || requests[0] != "/api/auth/config" || requests[1] != "/api/user" {
		t.Fatalf("unexpected verification requests: %v", requests)
	}
}

func TestAnonymousAdminLoginRefusesRequiredLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"data":{"login_required":true}}`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, AllowHTTP: true, AllowAnonymousAdmin: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Login(context.Background()); !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("expected ErrLoginRequired, got %v", err)
	}
}

func TestAnonymousAdminLoginRequiresAdminIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/auth/config" {
			_, _ = writer.Write([]byte(`{"success":true,"data":{"login_required":false}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"success":true,"data":{"id":"anonymous","name":"Guest","provider":"none","is_admin":false}}`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, AllowHTTP: true, AllowAnonymousAdmin: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Login(context.Background()); !errors.Is(err, ErrAdminRequired) {
		t.Fatalf("expected ErrAdminRequired, got %v", err)
	}
}

func TestAltMountAuthenticationModesAreExclusive(t *testing.T) {
	base := "http://altmount.invalid:8080"
	invalid := []Config{
		{BaseURL: base, AllowHTTP: true},
		{BaseURL: base, AllowHTTP: true, Username: "owner"},
		{BaseURL: base, AllowHTTP: true, Password: "password"},
		{BaseURL: base, AllowHTTP: true, Username: "owner", Password: "password", AllowAnonymousAdmin: true},
	}
	for _, config := range invalid {
		if _, err := New(config, nil); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("expected invalid config for %+v, got %v", config, err)
		}
	}
	if _, err := New(Config{BaseURL: base, AllowHTTP: true, Username: "owner", Password: "password"}, nil); err != nil {
		t.Fatalf("session credentials should remain valid: %v", err)
	}
	if _, err := New(Config{BaseURL: base, AllowHTTP: true, AllowAnonymousAdmin: true}, nil); err != nil {
		t.Fatalf("verified anonymous-admin mode should be valid: %v", err)
	}
}

func TestLiveAnonymousAdminOptIn(t *testing.T) {
	endpoint := os.Getenv("MEDIA_MANAGER_TEST_ALTMOUNT_URL")
	if endpoint == "" {
		t.Skip("MEDIA_MANAGER_TEST_ALTMOUNT_URL is not set")
	}
	client, err := New(Config{BaseURL: endpoint, AllowHTTP: true, AllowAnonymousAdmin: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Login(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !session.User.IsAdmin {
		t.Fatal("live AltMount session is not an admin")
	}
	if _, err := client.ListQueue(context.Background(), QueueQuery{Limit: 1}); err != nil {
		t.Fatalf("read-only queue probe failed: %v", err)
	}
}
