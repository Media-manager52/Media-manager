package plextv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPINLoginAndServers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Plex-Client-Identifier") != "mm-test" || request.Header.Get("X-Plex-Product") != "Media Manager" {
			t.Errorf("missing Plex client headers on %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v2/pins":
			if request.URL.Query().Get("strong") != "true" {
				t.Error("the PIN must be strong")
			}
			_, _ = writer.Write([]byte(`{"id":77,"code":"zyx","authToken":null,"expiresAt":"2026-11-05T12:30:00Z"}`))
		case request.URL.Path == "/api/v2/pins/77":
			_, _ = writer.Write([]byte(`{"id":77,"code":"zyx","authToken":"account-token"}`))
		case request.URL.Path == "/api/v2/resources":
			if request.Header.Get("X-Plex-Token") != "account-token" {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = writer.Write([]byte(`[{"name":"Hjemme","clientIdentifier":"abc","owned":true,"provides":"server","accessToken":"server-token","connections":[{"uri":"https://10-0-0-5.abc.plex.direct:32400","protocol":"https","local":true,"relay":false,"IPv6":false}]},{"name":"Telefon","clientIdentifier":"p","provides":"player","accessToken":"x"},{"name":"NoToken","clientIdentifier":"n","provides":"server,player"}]`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := New("mm-test", server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pin, err := client.CreatePIN(ctx)
	if err != nil || pin.ID != 77 || pin.Code != "zyx" || pin.AuthToken != "" {
		t.Fatalf("CreatePIN() = %+v, %v", pin, err)
	}
	authURL := client.AuthURL(pin.Code)
	fragment, _ := url.ParseQuery(strings.TrimPrefix(authURL[strings.Index(authURL, "#")+1:], "?"))
	if !strings.HasPrefix(authURL, "https://app.plex.tv/auth#?") || fragment.Get("clientID") != "mm-test" || fragment.Get("code") != "zyx" || fragment.Get("context[device][product]") != "Media Manager" {
		t.Fatalf("AuthURL() = %s", authURL)
	}
	pin, err = client.CheckPIN(ctx, 77)
	if err != nil || pin.AuthToken != "account-token" {
		t.Fatalf("CheckPIN() = %+v, %v", pin, err)
	}
	if _, err := client.CheckPIN(ctx, 78); !errors.Is(err, ErrInvalidPIN) {
		t.Fatalf("an unknown PIN = %v", err)
	}
	servers, err := client.Servers(ctx, "account-token")
	if err != nil || len(servers) != 1 || servers[0].ClientIdentifier != "abc" || servers[0].AccessToken != "server-token" || len(servers[0].Connections) != 1 || !servers[0].Connections[0].Local {
		t.Fatalf("Servers() = %+v, %v", servers, err)
	}
}

func TestIdentityProvesWhichServerAnswers(t *testing.T) {
	plex := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/identity" || request.Header.Get("X-Plex-Token") != "server-token" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = writer.Write([]byte(`{"MediaContainer":{"size":0,"claimed":true,"machineIdentifier":"abc","version":"1.43"}}`))
	}))
	defer plex.Close()
	id, err := Identity(context.Background(), plex.URL, "server-token", nil)
	if err != nil || id != "abc" {
		t.Fatalf("Identity() = %q, %v", id, err)
	}
	if _, err := Identity(context.Background(), plex.URL, "wrong", nil); err == nil {
		t.Fatal("a refused token was accepted")
	}
	if _, err := Identity(context.Background(), "ftp://plex", "server-token", nil); err == nil {
		t.Fatal("a non-http address was accepted")
	}
}
