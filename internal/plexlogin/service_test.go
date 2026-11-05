package plexlogin

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/plextv"
)

type fakeAccount struct {
	approved atomic.Bool
	gone     atomic.Bool
}

func (fake *fakeAccount) CreatePIN(context.Context) (plextv.PIN, error) {
	return plextv.PIN{ID: 42, Code: "abcd"}, nil
}

func (fake *fakeAccount) CheckPIN(_ context.Context, id int64) (plextv.PIN, error) {
	if fake.gone.Load() || id != 42 {
		return plextv.PIN{}, plextv.ErrInvalidPIN
	}
	if !fake.approved.Load() {
		return plextv.PIN{ID: 42, Code: "abcd"}, nil
	}
	return plextv.PIN{ID: 42, Code: "abcd", AuthToken: "account-token"}, nil
}

func (fake *fakeAccount) AuthURL(code string) string { return "https://app.plex.tv/auth#?code=" + code }

func (fake *fakeAccount) Servers(_ context.Context, token string) ([]plextv.Server, error) {
	if token != "account-token" {
		return nil, errors.New("wrong token")
	}
	return []plextv.Server{
		{Name: "Shared", ClientIdentifier: "shared-id", Owned: false, AccessToken: "shared-token", Connections: []plextv.Connection{{URI: "https://shared.example:32400"}}},
		{Name: "Hjemme", ClientIdentifier: "home-id", Owned: true, AccessToken: "home-token", Connections: []plextv.Connection{
			{URI: "https://relay.plex.direct:8443", Relay: true},
			{URI: "https://203-0-113-9.abc.plex.direct:32400"},
			{URI: "https://10-0-0-5.abc.plex.direct:32400", Local: true},
			{URI: "https://[2001:db8::1]:32400", IPv6: true},
		}},
	}, nil
}

func newTestService(identities map[string]string) (*Service, *fakeAccount) {
	account := &fakeAccount{}
	service := &Service{Account: account, Candidates: []string{"http://plex:32400", "http://other:32400"}, Identity: func(_ context.Context, uri, token string) (string, error) {
		if id, ok := identities[uri]; ok {
			if (id == "home-id" && token != "home-token") || (id == "shared-id" && token != "shared-token") {
				return "", errors.New("Plex returned HTTP 401")
			}
			return id, nil
		}
		return "", errors.New("not reachable from Media Manager")
	}}
	return service, account
}

func TestPlexLoginFindsTheBestReachableAddress(t *testing.T) {
	service, account := newTestService(map[string]string{
		"http://plex:32400":                         "home-id",
		"http://other:32400":                        "someone-else",
		"https://10-0-0-5.abc.plex.direct:32400":    "home-id",
		"https://203-0-113-9.abc.plex.direct:32400": "home-id",
		"https://relay.plex.direct:8443":            "home-id",
	})
	ctx := context.Background()
	started, err := service.Start(ctx)
	if err != nil || started.ID == "" || !strings.Contains(started.AuthURL, "code=abcd") {
		t.Fatalf("Start() = %+v, %v", started, err)
	}
	status, err := service.Poll(ctx, started.ID)
	if err != nil || status.State != "waiting" {
		t.Fatalf("Poll() before approval = %+v, %v", status, err)
	}
	if _, err := service.Choose(started.ID, "home-id", ""); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("Choose() before approval = %v", err)
	}

	account.approved.Store(true)
	status, err = service.Poll(ctx, started.ID)
	if err != nil || status.State != "approved" || len(status.Servers) != 2 {
		t.Fatalf("Poll() after approval = %+v, %v", status, err)
	}
	home := status.Servers[0]
	if home.ID != "home-id" || !home.Owned {
		t.Fatalf("owned servers come first: %+v", status.Servers)
	}
	var order []string
	for _, address := range home.Addresses {
		order = append(order, address.URI)
	}
	want := []string{"http://plex:32400", "https://10-0-0-5.abc.plex.direct:32400", "https://203-0-113-9.abc.plex.direct:32400", "https://relay.plex.direct:8443"}
	if strings.Join(order, " ") != strings.Join(want, " ") {
		t.Fatalf("addresses = %v, want %v (another server's container name and IPv6 dropped)", order, want)
	}
	if home.Recommended != "http://plex:32400" {
		t.Fatalf("recommended = %q", home.Recommended)
	}
	if shared := status.Servers[1]; shared.Recommended != "" || shared.Addresses[0].Reachable {
		t.Fatalf("an unreachable server was recommended: %+v", shared)
	}

	if _, err := service.Choose(started.ID, "shared-id", ""); !errors.Is(err, ErrUnknownServer) {
		t.Fatalf("an unreachable server was accepted: %v", err)
	}
	if _, err := service.Choose(started.ID, "home-id", "https://evil.example"); !errors.Is(err, ErrUnknownServer) {
		t.Fatalf("an address the login did not find was accepted: %v", err)
	}
	choice, err := service.Choose(started.ID, "home-id", "https://10-0-0-5.abc.plex.direct:32400")
	if err != nil || choice.Token != "home-token" || choice.AllowHTTP || choice.ServerName != "Hjemme" {
		t.Fatalf("Choose() = %+v, %v", choice, err)
	}
	if _, err := service.Poll(ctx, started.ID); !errors.Is(err, ErrUnknownLogin) {
		t.Fatalf("a used login was still pending: %v", err)
	}
}

func TestPlexLoginListsAServerWithoutAddressesAsEmpty(t *testing.T) {
	service := &Service{Candidates: []string{}, Identity: func(context.Context, string, string) (string, error) { return "", errors.New("unreachable") }}
	servers, _ := service.probe(context.Background(), []plextv.Server{{Name: "Ny", ClientIdentifier: "new", Owned: true, AccessToken: "t"}})
	if len(servers) != 1 || servers[0].Addresses == nil || servers[0].Recommended != "" {
		t.Fatalf("servers = %+v; addresses must be [] (not null) so the website can list them", servers)
	}
}

func TestPlexLoginExpires(t *testing.T) {
	service, account := newTestService(nil)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	service.Now = func() time.Time { return now }
	started, err := service.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(loginLifetime)
	if _, err := service.Poll(context.Background(), started.ID); !errors.Is(err, ErrUnknownLogin) {
		t.Fatalf("an expired login answered: %v", err)
	}
	now = now.Add(-loginLifetime)
	started, _ = service.Start(context.Background())
	account.gone.Store(true)
	if _, err := service.Poll(context.Background(), started.ID); !errors.Is(err, ErrUnknownLogin) {
		t.Fatalf("a login Plex forgot answered: %v", err)
	}
	if _, err := service.Poll(context.Background(), "nope"); !errors.Is(err, ErrUnknownLogin) {
		t.Fatal("an unknown id answered")
	}
}
