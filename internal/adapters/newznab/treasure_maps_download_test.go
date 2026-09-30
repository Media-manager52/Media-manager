package newznab

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
)

const (
	treasureMapsReleaseID = "1ff8990c3a2b4d5e6f708192a3b4c5d6"
	treasureMapsKey       = "treasure-maps-key-fixture"
)

// TestTreasureMapsReleaseIDDownloadContract pins the contract observed live on
// 2026-09-30: the feed advertises https://file.treasure-maps.com/getnzb/<id>?r=<key>
// for the same 32-hex ID as its /details/<id> permalink.
func TestTreasureMapsReleaseIDDownloadContract(t *testing.T) {
	t.Parallel()
	want := "https://file.treasure-maps.com/getnzb/" + treasureMapsReleaseID + "?r=" + treasureMapsKey
	for _, native := range []string{
		treasureMapsReleaseID,
		strings.ToUpper(treasureMapsReleaseID),
		"https://treasure-maps.com/details/" + treasureMapsReleaseID,
	} {
		target, known, err := releaseIDDownloadTarget(native, "https://treasure-maps.com/api", treasureMapsKey)
		if err != nil || !known || target == nil {
			t.Fatalf("native %q: known=%v err=%v", native, known, err)
		}
		if target.String() != want {
			t.Fatalf("native %q: target does not match the verified contract", native)
		}
	}

	for _, unsafe := range []struct{ native, base, key string }{
		{"not-a-release", "https://treasure-maps.com/api", treasureMapsKey},
		{treasureMapsReleaseID + "ab", "https://treasure-maps.com/api", treasureMapsKey},
		{"https://treasure-maps.com/details/" + treasureMapsReleaseID + "?x=1", "https://treasure-maps.com/api", treasureMapsKey},
		{"https://other.example/details/" + treasureMapsReleaseID, "https://treasure-maps.com/api", treasureMapsKey},
		{treasureMapsReleaseID, "http://treasure-maps.com/api", treasureMapsKey},
		{treasureMapsReleaseID, "https://treasure-maps.com:8443/api", treasureMapsKey},
		{treasureMapsReleaseID, "https://treasure-maps.com/api", ""},
	} {
		if target, known, err := releaseIDDownloadTarget(unsafe.native, unsafe.base, unsafe.key); !known || err == nil || target != nil {
			t.Fatalf("unsafe input %+v was not rejected: known=%v err=%v", unsafe, known, err)
		}
	}
}

// Every other production provider keeps its own path: advertised locators or t=get.
func TestReleaseIDDownloadContractLeavesOtherProvidersUnchanged(t *testing.T) {
	t.Parallel()
	for _, base := range []string{
		"https://drunkenslug.com/api",
		"https://nzbfinder.ws/api",
		"https://api.nzbgeek.info/api",
		"https://ninjacentral.co.za/api",
		"https://api.nzb.life/api",
		"https://abnzb.com/api",
		"https://api.althub.co.za/api",
		"https://msgnews.live/api",
		"https://file.treasure-maps.com/api",
		"https://www.treasure-maps.com/api",
	} {
		if target, known, err := releaseIDDownloadTarget(treasureMapsReleaseID, base, "key"); known || err != nil || target != nil {
			t.Fatalf("%s: release-ID contract applied to a provider without one", base)
		}
	}
}

type recordingTransport struct {
	mu       sync.Mutex
	requests []string
	respond  func(*http.Request) *http.Response
}

func (transport *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	transport.requests = append(transport.requests, request.URL.String())
	transport.mu.Unlock()
	return transport.respond(request), nil
}

func treasureMapsTestClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	scheduler := indexers.NewScheduler(1, nil)
	if err := scheduler.Configure("treasure-maps", indexers.Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IndexerID: "treasure-maps", BaseURL: "https://treasure-maps.com/api", APIKey: treasureMapsKey, Timeout: 5 * time.Second}, scheduler, transport)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func textResponse(request *http.Request, status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

// The provider answers t=get with a cross-host redirect carrying the key and
// its file host refuses requests without r=. Retrieval must go straight to the
// verified file-host link with the key: one request, no t=get, no redirect.
func TestTreasureMapsRetrievalRequestsTheVerifiedFileHostDirectly(t *testing.T) {
	t.Parallel()
	transport := &recordingTransport{respond: func(request *http.Request) *http.Response {
		switch {
		case request.URL.Host == "treasure-maps.com" && request.URL.Query().Get("t") == "get":
			return textResponse(request, http.StatusFound, "", http.Header{"Location": []string{"https://file.treasure-maps.com/getnzb?r=" + treasureMapsKey + "&id=" + treasureMapsReleaseID}})
		case request.URL.Host == "file.treasure-maps.com" && request.URL.Query().Get("r") == treasureMapsKey:
			return textResponse(request, http.StatusOK, redirectTestNZB, http.Header{"Content-Type": []string{"application/x-nzb"}})
		default:
			return textResponse(request, http.StatusForbidden, "<html>forbidden</html>", nil)
		}
	}}
	client := treasureMapsTestClient(t, transport)
	pack, err := client.GetNZBPackageFromLocatorGuarded(context.Background(), treasureMapsReleaseID, "native:"+treasureMapsReleaseID, nil)
	if err != nil {
		t.Fatalf("retrieval failed: %v (requests: %d)", err, len(transport.requests))
	}
	if len(pack.Body) == 0 {
		t.Fatal("no NZB body returned")
	}
	want := "https://file.treasure-maps.com/getnzb/" + treasureMapsReleaseID + "?r=" + treasureMapsKey
	if len(transport.requests) != 1 || transport.requests[0] != want {
		t.Fatalf("requests=%d, want exactly one request to the verified file-host link", len(transport.requests))
	}
}

func TestTreasureMapsRetrievalFailureDoesNotExposeTheKey(t *testing.T) {
	t.Parallel()
	transport := &recordingTransport{respond: func(request *http.Request) *http.Response {
		return textResponse(request, http.StatusForbidden, "<html>forbidden "+treasureMapsKey+"</html>", nil)
	}}
	client := treasureMapsTestClient(t, transport)
	_, err := client.GetNZBPackageFromLocatorGuarded(context.Background(), treasureMapsReleaseID, "native:"+treasureMapsReleaseID, nil)
	if err == nil {
		t.Fatal("a 403 from the file host must fail retrieval")
	}
	if strings.Contains(err.Error(), treasureMapsKey) {
		t.Fatal("retrieval error exposed the API key")
	}
}
