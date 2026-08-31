package newznab

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
)

func TestGetNZBGuardedStopsAfterPacingBeforeProviderRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	scheduler := indexers.NewScheduler(1, nil)
	if err := scheduler.Configure("guarded", indexers.Budget{MinimumInterval: 25 * time.Millisecond, FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	seed, err := scheduler.Acquire(context.Background(), "guarded", indexers.WorkloadRetrieval)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Record(context.Background(), indexers.OutcomeSuccess, nil); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IndexerID: "guarded", BaseURL: server.URL, APIKey: "secret", AllowHTTP: true}, scheduler, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	guardFailure := errors.New("workflow changed")
	started := time.Now()
	if _, err := client.GetNZBGuarded(context.Background(), "native-id", func(context.Context) error { return guardFailure }); !errors.Is(err, guardFailure) {
		t.Fatalf("guard error=%v", err)
	}
	if elapsed := time.Since(started); elapsed < 15*time.Millisecond {
		t.Fatalf("guard ran before scheduler wait completed: %s", elapsed)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("provider requests=%d, want 0", got)
	}
	if got := scheduler.Snapshot("guarded").LastOutcome; got != indexers.OutcomeCancelled {
		t.Fatalf("scheduler outcome=%q, want cancelled", got)
	}
}

func TestGetNZBRejectsUnsafeCrossHostRedirectWithoutExposingURL(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	redirectTarget := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, redirectTarget, http.StatusFound)
	}))
	defer source.Close()
	scheduler := indexers.NewScheduler(1, nil)
	if err := scheduler.Configure("redirect", indexers.Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IndexerID: "redirect", BaseURL: source.URL, APIKey: "secret", AllowHTTP: true}, scheduler, source.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetNZB(context.Background(), "native-id")
	var coded interface{ SafeCode() string }
	if !errors.As(err, &coded) || coded.SafeCode() != "newznab_unsafe_redirect_target_rejected" || strings.Contains(err.Error(), redirectTarget) {
		t.Fatalf("classified redirect error=%T %v", err, err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("cross-host redirect target requests=%d, want 0", got)
	}
}

func TestSearchStillRejectsCrossHostRedirect(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	redirectTarget := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, redirectTarget, http.StatusFound)
	}))
	defer source.Close()
	scheduler := indexers.NewScheduler(1, nil)
	if err := scheduler.Configure("search-redirect", indexers.Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IndexerID: "search-redirect", BaseURL: source.URL, APIKey: "secret", AllowHTTP: true}, scheduler, source.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Search(context.Background(), SearchRequest{Mode: "search"})
	var coded interface{ SafeCode() string }
	if !errors.As(err, &coded) || coded.SafeCode() != "newznab_cross_host_redirect_rejected" || strings.Contains(err.Error(), redirectTarget) {
		t.Fatalf("classified redirect error=%T %v", err, err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("cross-host redirect target requests=%d, want 0", got)
	}
}

func TestGetNZBFollowsValidatedHTTPSCrossHostRedirect(t *testing.T) {
	const validNZB = `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file poster="owner"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="12" number="1">message@example</segment></segments></file></nzb>`
	var targetRequests atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		targetRequests.Add(1)
		writer.Header().Set("Content-Type", "application/x-nzb")
		_, _ = writer.Write([]byte(validNZB))
	}))
	defer target.Close()
	redirectTarget := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, redirectTarget, http.StatusFound)
	}))
	defer source.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // Test servers use unrelated self-signed names.
	scheduler := indexers.NewScheduler(1, nil)
	if err := scheduler.Configure("validated-redirect", indexers.Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IndexerID: "validated-redirect", BaseURL: source.URL, APIKey: "secret", AllowHTTP: true}, scheduler, transport)
	if err != nil {
		t.Fatal(err)
	}
	var validations atomic.Int32
	client.validateNZBRedirectTarget = func(_ context.Context, target *url.URL) error {
		validations.Add(1)
		if target.Scheme != "https" || target.Hostname() != "localhost" {
			t.Fatalf("unexpected redirect target scheme=%q host=%q", target.Scheme, target.Hostname())
		}
		return nil
	}
	body, err := client.GetNZB(context.Background(), "native-id")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != validNZB || validations.Load() != 1 || targetRequests.Load() != 1 {
		t.Fatalf("body=%d bytes validations=%d target_requests=%d", len(body), validations.Load(), targetRequests.Load())
	}
}

func TestPublicInternetAddressRejectsNonPublicRanges(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "192.0.2.1", "::1", "fc00::1", "2001:db8::1"} {
		if publicInternetAddress(netip.MustParseAddr(value)) {
			t.Fatalf("non-public address accepted: %s", value)
		}
	}
	for _, value := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicInternetAddress(netip.MustParseAddr(value)) {
			t.Fatalf("public address rejected: %s", value)
		}
	}
}

func TestValidatedResponseControlsPersistedSchedulerOutcome(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantOutcome indexers.Outcome
		wantError   bool
	}{
		{name: "malformed success is failure", body: `<html><body>not Newznab</body></html>`, wantOutcome: indexers.OutcomeSystemFailure, wantError: true},
		{name: "provider auth error is auth failure", body: `<error code="100" description="Incorrect user credentials"/>`, wantOutcome: indexers.OutcomeAuthFailed, wantError: true},
		{name: "empty RSS is no results", body: `<rss><channel></channel></rss>`, wantOutcome: indexers.OutcomeNoResults},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			scheduler := indexers.NewScheduler(1, nil)
			if err := scheduler.Configure("test", indexers.Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
				t.Fatal(err)
			}
			client, err := New(Config{IndexerID: "test", BaseURL: server.URL, APIKey: "secret", AllowHTTP: true}, scheduler, server.Client().Transport)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Search(context.Background(), SearchRequest{Mode: "search"})
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v, wantError=%v", err, test.wantError)
			}
			if got := scheduler.Snapshot("test").LastOutcome; got != test.wantOutcome {
				t.Fatalf("outcome=%q, want %q", got, test.wantOutcome)
			}
		})
	}
}

func TestRetrievalIDPreservesIdentityAndSafelyExtractsDetailsID(t *testing.T) {
	tests := []struct {
		name      string
		nativeKey string
		baseURL   string
		want      string
		wantError bool
	}{
		{name: "plain provider ID", nativeKey: "0e415c0b-6943-4f00-88ab-415d70388531", baseURL: "https://ordinary.example/api", want: "0e415c0b-6943-4f00-88ab-415d70388531"},
		{name: "NZBFinder details permalink", nativeKey: "https://nzbfinder.ws/details/0e415c0b-6943-4f00-88ab-415d70388531", baseURL: "https://nzbfinder.ws/api", want: "0e415c0b-6943-4f00-88ab-415d70388531"},
		{name: "DrunkenSlug details permalink", nativeKey: "https://drunkenslug.com/details/0123456789abcdef0123456789abcdef01234567", baseURL: "https://drunkenslug.com/api", want: "0123456789abcdef0123456789abcdef01234567"},
		{name: "unverified provider permalink rejected", nativeKey: "https://ordinary.example/details/0e415c0b-6943-4f00-88ab-415d70388531", baseURL: "https://ordinary.example/api", wantError: true},
		{name: "trailing slash rejected", nativeKey: "https://nzbfinder.ws/details/0e415c0b-6943-4f00-88ab-415d70388531/", baseURL: "https://nzbfinder.ws/api", wantError: true},
		{name: "query rejected", nativeKey: "https://nzbfinder.ws/details/0e415c0b-6943-4f00-88ab-415d70388531?apikey=secret", baseURL: "https://nzbfinder.ws/api", wantError: true},
		{name: "userinfo rejected", nativeKey: "https://user:secret@nzbfinder.ws/details/0e415c0b-6943-4f00-88ab-415d70388531", baseURL: "https://nzbfinder.ws/api", wantError: true},
		{name: "other provider rejected", nativeKey: "https://other.example/details/0e415c0b-6943-4f00-88ab-415d70388531", baseURL: "https://nzbfinder.ws/api", wantError: true},
		{name: "scheme mismatch rejected", nativeKey: "http://nzbfinder.ws/details/0e415c0b-6943-4f00-88ab-415d70388531", baseURL: "https://nzbfinder.ws/api", wantError: true},
		{name: "non-details URL rejected", nativeKey: "https://nzbfinder.ws/download/0e415c0b-6943-4f00-88ab-415d70388531", baseURL: "https://nzbfinder.ws/api", wantError: true},
		{name: "encoded path rejected", nativeKey: "https://nzbfinder.ws/details/0e415c0b%2F6943-4f00-88ab-415d70388531", baseURL: "https://nzbfinder.ws/api", wantError: true},
		{name: "weak identity rejected", nativeKey: "weak:abcdef", baseURL: "https://ordinary.example/api", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := RetrievalID(test.nativeKey, test.baseURL)
			if (err != nil) != test.wantError || got != test.want {
				t.Fatalf("RetrievalID()=%q, %v; want %q, error=%v", got, err, test.want, test.wantError)
			}
		})
	}
}
