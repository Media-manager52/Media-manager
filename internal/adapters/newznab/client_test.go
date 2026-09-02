package newznab

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/releases"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestSearchEncodesExactXMLWireRequest(t *testing.T) {
	t.Parallel()
	const apiKey = "characterization-api-key"
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != "indexer.invalid" || request.URL.Path != "/api" {
			t.Fatalf("unexpected request target: %s %s://%s%s", request.Method, request.URL.Scheme, request.URL.Host, request.URL.Path)
		}
		if accept := request.Header.Get("Accept"); !strings.Contains(accept, "application/xml") {
			t.Fatalf("Accept=%q, want XML", accept)
		}
		query := request.URL.Query()
		want := map[string]string{
			"apikey":   apiKey,
			"o":        "xml",
			"t":        "tvsearch",
			"q":        "Example Show",
			"tvdbid":   "12345",
			"season":   "2",
			"cat":      "5000,5040",
			"limit":    "100",
			"offset":   "200",
			"extended": "1",
		}
		if len(query) != len(want) {
			t.Fatalf("query keys=%v, want %d exact keys", queryKeys(query), len(want))
		}
		for name, value := range want {
			if values := query[name]; len(values) != 1 || values[0] != value {
				t.Fatalf("query[%q]=%v, want [%q]", name, values, value)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`<rss><channel></channel></rss>`)),
			Request:    request,
		}, nil
	})
	scheduler := indexers.NewScheduler(1, nil)
	if err := scheduler.Configure("wire-request", indexers.Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IndexerID: "wire-request", BaseURL: "https://indexer.invalid/api", APIKey: apiKey}, scheduler, transport)
	if err != nil {
		t.Fatal(err)
	}
	observations, err := client.Search(context.Background(), SearchRequest{
		Mode:       "tvsearch",
		Params:     []Param{{Name: "q", Value: "Example Show"}, {Name: "tvdbid", Value: "12345"}, {Name: "season", Value: "2"}},
		Categories: []int{5000, 5040},
		Limit:      100,
		Offset:     200,
		Extended:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 0 {
		t.Fatalf("observations=%d, want 0", len(observations))
	}
}

func TestGetNZBGuardedDefersPacingBeforeGuardAndProviderRequest(t *testing.T) {
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
	if _, err := client.GetNZBGuarded(context.Background(), "native-id", func(context.Context) error { return guardFailure }); err == nil {
		t.Fatal("pacing deferral was not returned")
	} else {
		var deferred indexers.CooldownError
		if !errors.As(err, &deferred) {
			t.Fatalf("pacing error=%v", err)
		}
	}
	if elapsed := time.Since(started); elapsed > 15*time.Millisecond {
		t.Fatalf("pacing deferral blocked for %s", elapsed)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("provider requests=%d, want 0", got)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := client.GetNZBGuarded(context.Background(), "native-id", func(context.Context) error { return guardFailure }); !errors.Is(err, guardFailure) {
		t.Fatalf("guard error after pacing deadline=%v", err)
	}
	if got := scheduler.Snapshot("guarded").LastOutcome; got != indexers.OutcomeCancelled {
		t.Fatalf("scheduler outcome=%q, want cancelled", got)
	}
}

func TestBackgroundSearchGuardStopsBeforeProviderRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("request must not be sent")
	})
	scheduler := indexers.NewScheduler(1, nil)
	if err := scheduler.Configure("background-guard", indexers.Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute, QueryLimit: 100, LimitWindow: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IndexerID: "background-guard", BaseURL: "https://indexer.invalid/api", APIKey: "secret"}, scheduler, transport)
	if err != nil {
		t.Fatal(err)
	}
	guardFailure := errors.New("discovery generation changed")
	guardCalls := 0
	if _, err := client.BackgroundSearchGuarded(context.Background(), SearchRequest{Mode: "search"}, func(context.Context) error {
		guardCalls++
		return guardFailure
	}); !errors.Is(err, guardFailure) {
		t.Fatalf("guard error=%v", err)
	}
	if guardCalls != 1 || requests.Load() != 0 {
		t.Fatalf("guard calls=%d provider requests=%d", guardCalls, requests.Load())
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
	if !errors.As(err, &coded) || coded.SafeCode() != "newznab_redirect_url_policy_rejected" || strings.Contains(err.Error(), redirectTarget) {
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

func TestDrunkenSlugAdvertisedDownloadUsesSanitizedLocator(t *testing.T) {
	t.Parallel()
	const releaseID = "0123456789abcdef0123456789abcdef01234567"
	const apiKey = "test-provider-key"
	const sanitizedLocator = "https://drunkenslug.com/getnzb/" + releaseID + ".nzb&i=12345"
	var requests atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		if request.URL.Host != "drunkenslug.com" || request.URL.Path != "/getnzb/"+releaseID+".nzb&i=12345&r="+apiKey || request.URL.RawQuery != "" {
			t.Fatal("advertised locator did not produce the exact provider request shape")
		}
		if request.Referer() != "" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatal("advertised locator request carried origin-specific headers")
		}
		body := `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file poster="test" subject="test"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="12" number="1">message@example</segment></segments></file></nzb>`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	scheduler := indexers.NewScheduler(1, nil)
	if err := scheduler.Configure("drunkenslug", indexers.Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IndexerID: "drunkenslug", BaseURL: "https://drunkenslug.com/api", APIKey: apiKey}, scheduler, transport)
	if err != nil {
		t.Fatal(err)
	}
	guardCalls := 0
	locatorReference := DownloadLocatorReference(sanitizedLocator, "https://drunkenslug.com/details/"+releaseID)
	pack, err := client.GetNZBPackageFromLocatorGuarded(context.Background(), releaseID, locatorReference, func(context.Context) error {
		guardCalls++
		return nil
	})
	if err != nil || len(pack.Body) == 0 {
		t.Fatalf("advertised locator retrieval failed: %v", err)
	}
	if requests.Load() != 1 || guardCalls != 1 {
		t.Fatalf("requests=%d guard calls=%d, want 1 and 1", requests.Load(), guardCalls)
	}
}

func TestDrunkenSlugAdvertisedDownloadRejectsLocatorMismatch(t *testing.T) {
	t.Parallel()
	const releaseID = "0123456789abcdef0123456789abcdef01234567"
	wrong := "advertised:https://drunkenslug.com/getnzb/ffffffffffffffffffffffffffffffffffffffff.nzb&i=12345"
	if _, advertised, err := advertisedDownloadTarget(wrong, "https://drunkenslug.com/details/"+releaseID, "https://drunkenslug.com/api", "secret"); !advertised || err == nil {
		t.Fatal("mismatched advertised locator was not rejected")
	}
}

func TestNZBFinderAdvertisedDownloadUsesSanitizedLocator(t *testing.T) {
	t.Parallel()
	const releaseID = "0e415c0b-6943-4f00-88ab-415d70388531"
	const apiKey = "test-provider-key"
	advertised := "https://nzbfinder.ws/api/v1/getnzb?id=" + releaseID + ".nzb&apikey=" + apiKey
	var requests atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		if request.URL.Host != "nzbfinder.ws" || request.URL.Path != "/api/v1/getnzb" {
			t.Fatal("advertised locator did not retain the exact NZBFinder origin and path")
		}
		query := request.URL.Query()
		if len(query) != 2 || len(query["id"]) != 1 || query.Get("id") != releaseID+".nzb" || len(query["apikey"]) != 1 || query.Get("apikey") != apiKey {
			t.Fatal("advertised locator did not produce the exact NZBFinder query shape")
		}
		if query.Has("t") || request.Referer() != "" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Fatal("advertised locator request carried an unsupported parameter or origin-specific header")
		}
		body := `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file poster="test" subject="test"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="12" number="1">message@example</segment></segments></file></nzb>`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	scheduler := indexers.NewScheduler(1, nil)
	if err := scheduler.Configure("nzbfinder", indexers.Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IndexerID: "nzbfinder", BaseURL: "https://nzbfinder.ws/api", APIKey: apiKey}, scheduler, transport)
	if err != nil {
		t.Fatal(err)
	}
	native := "https://nzbfinder.ws/details/" + releaseID
	locatorReference := DownloadLocatorReference(advertised, native)
	if strings.Contains(locatorReference, apiKey) {
		t.Fatal("durable locator retained the provider credential")
	}
	guardCalls := 0
	pack, err := client.GetNZBPackageFromLocatorGuarded(context.Background(), releaseID, locatorReference, func(context.Context) error {
		guardCalls++
		return nil
	})
	if err != nil || len(pack.Body) == 0 {
		t.Fatalf("advertised locator retrieval failed: %v", err)
	}
	if requests.Load() != 1 || guardCalls != 1 {
		t.Fatalf("requests=%d guard calls=%d, want 1 and 1", requests.Load(), guardCalls)
	}
}

func TestNZBFinderAdvertisedDownloadRejectsUnverifiedLocators(t *testing.T) {
	t.Parallel()
	const releaseID = "0e415c0b-6943-4f00-88ab-415d70388531"
	native := "https://nzbfinder.ws/details/" + releaseID
	valid := "https://nzbfinder.ws/api/v1/getnzb?id=" + releaseID + ".nzb&apikey=must-not-persist"
	if got := DownloadLocatorReference(valid, native); got != "advertised:https://nzbfinder.ws/api/v1/getnzb?id="+releaseID+".nzb" {
		t.Fatal("sanitized NZBFinder locator was not retained")
	}
	for _, unsafe := range []string{
		"http://nzbfinder.ws/api/v1/getnzb?id=" + releaseID + ".nzb",
		"https://nzbfinder.ws:444/api/v1/getnzb?id=" + releaseID + ".nzb",
		"https://other.example/api/v1/getnzb?id=" + releaseID + ".nzb",
		"https://nzbfinder.ws/api/v1/getnzb?id=ffffffff-ffff-ffff-ffff-ffffffffffff.nzb",
		"https://nzbfinder.ws/api/v1/getnzb?id=" + releaseID + ".nzb&id=" + releaseID + ".nzb",
		"https://nzbfinder.ws/api/v1/getnzb?id=" + releaseID + ".nzb&ticket=unexpected",
		"https://nzbfinder.ws/api/v1/getnzb?id=" + releaseID,
		"https://nzbfinder.ws/api?t=get&id=" + releaseID,
	} {
		if got := DownloadLocatorReference(unsafe, native); got != "native:"+native {
			t.Fatal("unverified NZBFinder locator was retained")
		}
	}
	wrongOrigin := "advertised:https://nzbfinder.ws:444/api/v1/getnzb?id=" + releaseID + ".nzb"
	if _, advertised, err := advertisedDownloadTarget(wrongOrigin, native, "https://nzbfinder.ws/api", "secret"); !advertised || err == nil {
		t.Fatal("wrong-origin advertised NZBFinder locator was not rejected")
	}
}

func TestObservedSameOriginPathDownloadLocatorsAreReconstructedInMemory(t *testing.T) {
	t.Parallel()
	const releaseID = "2064db7970379fcd33484c55071467ef"
	const apiKey = "test-provider-key"
	for _, test := range []struct {
		name string
		host string
	}{
		{name: "abNZB", host: "abnzb.com"},
		{name: "altHUB", host: "api.althub.co.za"},
		{name: "msgnews", host: "msgnews.live"},
		{name: "NinjaCentral", host: "ninjacentral.co.za"},
		{name: "Nzb.life", host: "api.nzb.life"},
	} {
		t.Run(test.name, func(t *testing.T) {
			advertisedURL := "https://" + test.host + "/getnzb/" + releaseID + ".nzb&i=219&r=" + apiKey
			sanitized := SanitizeObservationWithCredential(releases.Observation{DownloadURL: advertisedURL}, apiKey)
			locatorReference := DownloadLocatorReference(sanitized.DownloadURL, releaseID)
			if !strings.HasPrefix(locatorReference, advertisedLocatorPrefix) || strings.Contains(locatorReference, apiKey) {
				t.Fatal("verified locator was not retained without its credential")
			}
			target, advertised, err := advertisedDownloadTarget(locatorReference, releaseID, "https://"+test.host+"/api", apiKey)
			if err != nil || !advertised {
				t.Fatalf("advertised target=%v err=%v", advertised, err)
			}
			if target.Host != test.host || target.Path != "/getnzb/"+releaseID+".nzb&i=219&r="+apiKey || target.RawQuery != "" {
				t.Fatal("provider path locator was not reconstructed exactly")
			}
		})
	}
}

func TestSameOriginPathDownloadLocatorRejectsMismatchAndExtraParameters(t *testing.T) {
	t.Parallel()
	const releaseID = "2064db7970379fcd33484c55071467ef"
	fallback := "native:" + releaseID
	for _, unsafe := range []string{
		"https://msgnews.live/getnzb/ffffffffffffffffffffffffffffffff.nzb&i=219",
		"https://msgnews.live/getnzb/" + releaseID + ".nzb&i=219?ticket=unexpected",
		"https://msgnews.live:444/getnzb/" + releaseID + ".nzb&i=219",
		"https://other.example/getnzb/" + releaseID + ".nzb&i=219",
	} {
		if got := DownloadLocatorReference(unsafe, releaseID); got != fallback {
			t.Fatal("unverified same-origin path locator was retained")
		}
	}
}

func TestDownloadLocatorReferenceKeepsOnlyCredentialFreeDrunkenSlugShape(t *testing.T) {
	t.Parallel()
	const releaseID = "0123456789abcdef0123456789abcdef01234567"
	native := "https://drunkenslug.com/details/" + releaseID
	valid := "https://drunkenslug.com/getnzb/" + releaseID + ".nzb&i=12345"
	if got := DownloadLocatorReference(valid, native); got != "advertised:"+valid {
		t.Fatal("credential-free verified locator was not retained")
	}
	for _, unsafe := range []string{
		valid + "&r=must-not-persist",
		"https://other.example/getnzb/" + releaseID + ".nzb&i=12345",
		"https://drunkenslug.com/getnzb/ffffffffffffffffffffffffffffffffffffffff.nzb&i=12345",
	} {
		if got := DownloadLocatorReference(unsafe, native); got != "native:"+native {
			t.Fatal("unverified or credential-bearing locator was retained")
		}
	}
}

func TestCrossOriginRedirectRejectsCredentialInPath(t *testing.T) {
	t.Parallel()
	client := &Client{config: Config{APIKey: "source-secret"}}
	previous, _ := url.Parse("https://drunkenslug.com/getnzb/release.nzb&i=1&r=source-secret")
	for _, location := range []string{
		"https://downloads.example/file/source-secret/package.nzb",
		"https://downloads.example/file/source%2Dsecret/package.nzb",
	} {
		response := &http.Response{Header: http.Header{"Location": []string{location}}}
		if _, err := client.redirectTarget(previous, response); !errors.Is(err, ErrRedirectURLPolicy) {
			t.Fatal("cross-origin redirect carrying a source credential in its path was not rejected")
		}
	}
}
