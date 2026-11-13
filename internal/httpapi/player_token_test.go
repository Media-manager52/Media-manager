package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/media-manager/media-manager/internal/postgres"
)

// playerRoutes is the complete list of routes a playback client may call with
// the player token. Adding a route here widens what a leaked player token can
// do, so it needs the same review as any other credential change.
var playerRoutes = map[string]bool{
	"GET /api/v1/media":                        true,
	"POST /api/v1/media":                       true,
	"GET /api/v1/media/{id}/detail":            true,
	"POST /api/v1/media/{id}/workflows":        true,
	"GET /api/v1/files/{id}":                   true,
	"GET /api/v1/discovery/search":             true,
	"GET /api/v1/discovery/{kind}/{id}":        true,
	"POST /api/v1/streaming/search":            true,
	"POST /api/v1/streaming/releases/{id}/nzb": true,
	// Prepare returns only a path on the read-only mount the addon already
	// reads; it hands out no AltMount credential or download key.
	"POST /api/v1/streaming/releases/{id}/prepare": true,
}

func TestPlayerTokenOpensOnlyThePlaybackRoutes(t *testing.T) {
	owner, player := strings.Repeat("o", 32), strings.Repeat("p", 32)
	server, err := New(Config{OwnerToken: owner, PlayerToken: player, Version: "test"}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, operation := range server.apiOperations() {
		key := operation.Method + " " + operation.Path
		if operation.Auth == authOwnerOrPlayer {
			seen[key] = true
			if !playerRoutes[key] {
				t.Errorf("%s accepts the player token but is not in the reviewed player route list", key)
			}
			continue
		}
		if operation.Auth != authOwner {
			continue
		}
		path := strings.NewReplacer("{id}", "01a0551d-37ec-767c-9213-f56723dd6035", "{kind}", "movie", "{run}", "01a0551d-37ec-767c-9213-f56723dd6035", "{step}", "01a0551d-37ec-767c-9213-f56723dd6035").Replace(operation.Path)
		request := httptest.NewRequest(operation.Method, path, strings.NewReader("{}"))
		request.Header.Set("Authorization", "Bearer "+player)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s answered %d to the player token, want 401", key, response.Code)
		}
	}
	for key := range playerRoutes {
		if !seen[key] {
			t.Errorf("reviewed player route %s is not marked owner_or_player", key)
		}
	}
}

func TestPlayerRoutesStillRefuseMissingOrWrongTokens(t *testing.T) {
	owner, player := strings.Repeat("o", 32), strings.Repeat("p", 32)
	for name, config := range map[string]Config{
		"player configured":     {OwnerToken: owner, PlayerToken: player},
		"player not configured": {OwnerToken: owner},
	} {
		server, err := New(config, &postgres.Store{})
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range []string{"", "Bearer ", "Bearer " + strings.Repeat("x", 32), player} {
			if config.PlayerToken == player && token == player {
				continue
			}
			request := httptest.NewRequest("GET", "/api/v1/files/01a0551d-37ec-767c-9213-f56723dd6035", nil)
			if token != "" {
				request.Header.Set("Authorization", token)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Errorf("%s: authorization %q answered %d, want 401", name, token, response.Code)
			}
		}
		// A bare player token without the Bearer prefix is never accepted.
		request := httptest.NewRequest("GET", "/api/v1/files/01a0551d-37ec-767c-9213-f56723dd6035", nil)
		request.Header.Set("Authorization", player)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("%s: bare token answered %d, want 401", name, response.Code)
		}
	}
}

func TestPlayerTokenReachesAPlaybackHandler(t *testing.T) {
	owner, player := strings.Repeat("o", 32), strings.Repeat("p", 32)
	server, err := New(Config{OwnerToken: owner, PlayerToken: player, Version: "test"}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	// No metadata provider is configured, so the handler itself answers; the
	// point is that authorization let the player through.
	request := httptest.NewRequest("GET", "/api/v1/discovery/search?query=test", nil)
	request.Header.Set("Authorization", "Bearer "+player)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusUnauthorized {
		t.Fatalf("player token was refused on a playback route: %s", response.Body.String())
	}
}
