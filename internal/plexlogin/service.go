// Package plexlogin connects Media Manager to a Plex Media Server by signing
// in to the owner's Plex account ("Log ind med Plex"). The owner approves the
// login on app.plex.tv; Media Manager receives the token, finds the account's
// servers, proves which of their addresses it can reach, and saves the chosen
// one as a Plex connection with the token sealed. The token is held in memory
// only until then and is never sent to the browser.
package plexlogin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/media-manager/media-manager/internal/adapters/plextv"
)

// ErrUnknownLogin means the login id is unknown, expired or already used.
var ErrUnknownLogin = errors.New("the Plex login is unknown or has expired; start it again")

// ErrNotApproved means the owner has not approved the login on Plex yet.
var ErrNotApproved = errors.New("the Plex login has not been approved yet")

// ErrUnknownServer means the chosen server or address is not one the login found.
var ErrUnknownServer = errors.New("choose one of the Plex servers and addresses Media Manager found")

const loginLifetime = 20 * time.Minute

// Candidates are addresses tried besides the ones Plex advertises: the
// usual name of a Plex container on the same Docker network. One is used
// only when the server answering there has the chosen server's identity.
var defaultCandidates = []string{"http://plex:32400"}

// Account is the part of plex.tv the service uses.
type Account interface {
	CreatePIN(ctx context.Context) (plextv.PIN, error)
	CheckPIN(ctx context.Context, id int64) (plextv.PIN, error)
	AuthURL(code string) string
	Servers(ctx context.Context, token string) ([]plextv.Server, error)
}

// Service holds pending Plex logins.
type Service struct {
	Account Account
	// Identity reads the machine identifier of the Plex server at uri.
	Identity func(ctx context.Context, uri, token string) (string, error)
	// Candidates overrides defaultCandidates (tests).
	Candidates []string
	Now        func() time.Time

	mutex  sync.Mutex
	logins map[string]*login
}

type login struct {
	pinID     int64
	expiresAt time.Time
	token     string
	servers   []Server
	tokens    map[string]string // server id -> access token
}

// Started is a new login for the browser to open.
type Started struct {
	ID        string    `json:"id"`
	AuthURL   string    `json:"auth_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Address is one way Media Manager could reach a server.
type Address struct {
	URI       string `json:"uri"`
	Local     bool   `json:"local"`
	Relay     bool   `json:"relay"`
	Reachable bool   `json:"reachable"`
	Detail    string `json:"detail,omitempty"`
}

// Server is a Plex Media Server on the account. Addresses are ordered best
// first; Recommended is the first reachable one, or "".
type Server struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Owned       bool      `json:"owned"`
	Addresses   []Address `json:"addresses"`
	Recommended string    `json:"recommended,omitempty"`
}

// Status is a login's progress: "waiting" until the owner approves it on
// Plex, then "approved" with the servers found.
type Status struct {
	State   string   `json:"state"`
	Servers []Server `json:"servers"`
}

// Choice is what Connect saves: an address proven reachable and the token
// for that server.
type Choice struct {
	ServerName string
	URI        string
	AllowHTTP  bool
	Token      string
}

func (service *Service) now() time.Time {
	if service.Now != nil {
		return service.Now()
	}
	return time.Now()
}

// Start creates a Plex PIN and returns where the owner approves it.
func (service *Service) Start(ctx context.Context) (Started, error) {
	if service == nil || service.Account == nil {
		return Started{}, errors.New("Plex login is unavailable")
	}
	pin, err := service.Account.CreatePIN(ctx)
	if err != nil {
		return Started{}, err
	}
	id, err := randomID()
	if err != nil {
		return Started{}, err
	}
	expires := service.now().Add(loginLifetime)
	if !pin.ExpiresAt.IsZero() && pin.ExpiresAt.Before(expires) {
		expires = pin.ExpiresAt
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	service.pruneLocked()
	if service.logins == nil {
		service.logins = map[string]*login{}
	}
	if len(service.logins) >= 20 {
		return Started{}, errors.New("too many Plex logins are pending; wait a few minutes")
	}
	service.logins[id] = &login{pinID: pin.ID, expiresAt: expires}
	return Started{ID: id, AuthURL: service.Account.AuthURL(pin.Code), ExpiresAt: expires.UTC()}, nil
}

// Poll reports whether the owner approved the login. On approval it lists
// the account's servers and checks every address once.
func (service *Service) Poll(ctx context.Context, id string) (Status, error) {
	entry, err := service.entry(id)
	if err != nil {
		return Status{}, err
	}
	service.mutex.Lock()
	if entry.token != "" {
		servers := entry.servers
		service.mutex.Unlock()
		return Status{State: "approved", Servers: servers}, nil
	}
	pinID := entry.pinID
	service.mutex.Unlock()

	pin, err := service.Account.CheckPIN(ctx, pinID)
	if errors.Is(err, plextv.ErrInvalidPIN) {
		service.forget(id)
		return Status{}, ErrUnknownLogin
	}
	if err != nil {
		return Status{}, err
	}
	token := strings.TrimSpace(pin.AuthToken)
	if token == "" {
		return Status{State: "waiting", Servers: []Server{}}, nil
	}
	resources, err := service.Account.Servers(ctx, token)
	if err != nil {
		return Status{}, err
	}
	servers, tokens := service.probe(ctx, resources)

	service.mutex.Lock()
	defer service.mutex.Unlock()
	if current, ok := service.logins[id]; ok && current == entry {
		entry.token, entry.servers, entry.tokens = token, servers, tokens
	}
	return Status{State: "approved", Servers: servers}, nil
}

// Choose returns the saved address and token for serverID. uri, when set,
// must be one of the server's reachable addresses; otherwise the
// recommended address is used. The login is consumed.
func (service *Service) Choose(id, serverID, uri string) (Choice, error) {
	entry, err := service.entry(id)
	if err != nil {
		return Choice{}, err
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if entry.token == "" {
		return Choice{}, ErrNotApproved
	}
	for _, server := range entry.servers {
		if server.ID != serverID {
			continue
		}
		chosen := strings.TrimSpace(uri)
		if chosen == "" {
			chosen = server.Recommended
		}
		for _, address := range server.Addresses {
			if address.URI == chosen && address.Reachable {
				delete(service.logins, id)
				return Choice{ServerName: server.Name, URI: address.URI, AllowHTTP: strings.HasPrefix(address.URI, "http://"), Token: entry.tokens[server.ID]}, nil
			}
		}
		return Choice{}, ErrUnknownServer
	}
	return Choice{}, ErrUnknownServer
}

func (service *Service) entry(id string) (*login, error) {
	if service == nil {
		return nil, ErrUnknownLogin
	}
	service.mutex.Lock()
	defer service.mutex.Unlock()
	service.pruneLocked()
	entry, ok := service.logins[id]
	if !ok {
		return nil, ErrUnknownLogin
	}
	return entry, nil
}

func (service *Service) forget(id string) {
	service.mutex.Lock()
	delete(service.logins, id)
	service.mutex.Unlock()
}

func (service *Service) pruneLocked() {
	now := service.now()
	for id, entry := range service.logins {
		if !now.Before(entry.expiresAt) {
			delete(service.logins, id)
		}
	}
}

// probe checks every address of every server concurrently.
func (service *Service) probe(ctx context.Context, resources []plextv.Server) ([]Server, map[string]string) {
	candidates := service.Candidates
	if candidates == nil {
		candidates = defaultCandidates
	}
	servers := make([]Server, len(resources))
	tokens := map[string]string{}
	var wait sync.WaitGroup
	for index, resource := range resources {
		server := Server{ID: resource.ClientIdentifier, Name: resource.Name, Owned: resource.Owned}
		tokens[resource.ClientIdentifier] = resource.AccessToken
		seen := map[string]bool{}
		for _, candidate := range candidates {
			if !seen[candidate] {
				seen[candidate] = true
				server.Addresses = append(server.Addresses, Address{URI: candidate, Local: true})
			}
		}
		for _, connection := range resource.Connections {
			uri := strings.TrimRight(strings.TrimSpace(connection.URI), "/")
			if uri == "" || seen[uri] || connection.IPv6 {
				continue
			}
			seen[uri] = true
			server.Addresses = append(server.Addresses, Address{URI: uri, Local: connection.Local, Relay: connection.Relay})
		}
		servers[index] = server
		for addressIndex := range server.Addresses {
			wait.Add(1)
			go func(server *Server, address *Address, want, token string) {
				defer wait.Done()
				got, err := service.Identity(ctx, address.URI, token)
				switch {
				case err != nil:
					address.Detail = err.Error()
				case got != want:
					address.Detail = "another Plex server answers here"
				default:
					address.Reachable = true
				}
			}(&servers[index], &servers[index].Addresses[addressIndex], resource.ClientIdentifier, resource.AccessToken)
		}
	}
	wait.Wait()
	for index := range servers {
		server := &servers[index]
		// Keep the named candidates only when they reach this server.
		kept := server.Addresses[:0]
		for _, address := range server.Addresses {
			if isCandidate(address.URI, candidates) && !address.Reachable {
				continue
			}
			kept = append(kept, address)
		}
		server.Addresses = kept
		sort.SliceStable(server.Addresses, func(i, j int) bool {
			return rank(server.Addresses[i], candidates) < rank(server.Addresses[j], candidates)
		})
		for _, address := range server.Addresses {
			if address.Reachable {
				server.Recommended = address.URI
				break
			}
		}
	}
	sort.SliceStable(servers, func(i, j int) bool { return servers[i].Owned && !servers[j].Owned })
	return servers, tokens
}

// rank orders addresses: reachable first; then a container name, a direct
// local address, a direct remote address, and a relay last (relays are slow
// and limited).
func rank(address Address, candidates []string) int {
	score := 0
	if !address.Reachable {
		score += 10
	}
	switch {
	case isCandidate(address.URI, candidates):
	case address.Relay:
		score += 3
	case address.Local:
		score += 1
	default:
		score += 2
	}
	return score
}

func isCandidate(uri string, candidates []string) bool {
	for _, candidate := range candidates {
		if candidate == uri {
			return true
		}
	}
	return false
}

func randomID() (string, error) {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer[:]), nil
}

// NewClientIdentifier returns a random Plex client identifier for this
// Media Manager process.
func NewClientIdentifier() string {
	id, err := randomID()
	if err != nil {
		return "media-manager"
	}
	return "media-manager-" + id
}

// New returns a service that talks to plex.tv.
func New(transport http.RoundTripper) (*Service, error) {
	account, err := plextv.New(NewClientIdentifier(), "", transport)
	if err != nil {
		return nil, err
	}
	return &Service{Account: account, Identity: func(ctx context.Context, uri, token string) (string, error) {
		return plextv.Identity(ctx, uri, token, transport)
	}}, nil
}
