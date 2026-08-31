package newznab

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
)

const redirectTestNZB = `<?xml version="1.0"?><nzb xmlns="http://www.newzbin.com/DTD/2003/nzb"><file poster="owner"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="12" number="1">message@example</segment></segments></file></nzb>`

func TestGoAutomaticRedirectsAreDisabled(t *testing.T) {
	client, _ := newRedirectTestClient(t, "automatic-disabled", "http://indexer.test/api", nil, nil)
	request, err := http.NewRequest(http.MethodGet, "http://indexer.test/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.http.CheckRedirect(request, []*http.Request{request}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("automatic redirect callback error=%v", err)
	}
}

func TestGetNZBRedirectPinsValidatedAddressAgainstDNSRebindingAndPreservesTLSName(t *testing.T) {
	var sni atomic.Value
	var targetRequests atomic.Int32
	target, roots := newNamedTLSServer(t, []string{"download.test"}, func(writer http.ResponseWriter, request *http.Request) {
		targetRequests.Add(1)
		if request.URL.Query().Get("ticket") != "one" || request.URL.Query().Has("apikey") {
			t.Errorf("redirect query was not isolated: keys=%v", queryKeys(request.URL.Query()))
		}
		for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Referer", "X-Indexer-Key"} {
			if request.Header.Get(header) != "" {
				t.Errorf("redirect leaked %s", header)
			}
		}
		writer.Header().Set("Content-Type", "application/x-nzb")
		_, _ = writer.Write([]byte(redirectTestNZB))
	}, func(name string) { sni.Store(name) })
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("apikey") != "source-api-key-fixture" {
			t.Error("source did not receive its configured API key")
		}
		writer.Header().Set("Location", "https://DOWNLOAD.TEST.:443/files/release.nzb?ticket=one&apikey=source-api-key-fixture")
		writer.WriteHeader(http.StatusFound)
	}))
	defer source.Close()

	client, scheduler := newRedirectTestClient(t, "pinned", source.URL, roots, nil)
	var resolutions atomic.Int32
	client.resolveRedirectAddresses = func(_ context.Context, hostname string) ([]netip.Addr, error) {
		resolution := resolutions.Add(1)
		if hostname != "download.test" {
			t.Fatalf("resolved hostname=%q", hostname)
		}
		if resolution > 1 {
			return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}
	var dialed atomic.Value
	client.dialPinnedAddress = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed.Store(address)
		if address != "1.1.1.1:443" {
			t.Fatalf("dial address=%q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}

	body, err := client.GetNZB(context.Background(), "native-id")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != redirectTestNZB {
		t.Fatalf("body=%d bytes", len(body))
	}
	if resolutions.Load() != 1 || targetRequests.Load() != 1 || dialed.Load() != "1.1.1.1:443" {
		t.Fatalf("resolutions=%d target_requests=%d dialed=%v", resolutions.Load(), targetRequests.Load(), dialed.Load())
	}
	if got, _ := sni.Load().(string); got != "download.test" {
		t.Fatalf("TLS SNI=%q", got)
	}
	if scheduler.Snapshot("pinned").LastOutcome != indexers.OutcomeSuccess {
		t.Fatalf("scheduler outcome=%q", scheduler.Snapshot("pinned").LastOutcome)
	}
}

func TestGetNZBRedirectRejectsMixedPublicAndPrivateAnswersBeforeDial(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "https://download.test/file.nzb")
		writer.WriteHeader(http.StatusFound)
	}))
	defer source.Close()
	client, scheduler := newRedirectTestClient(t, "mixed", source.URL, nil, nil)
	client.resolveRedirectAddresses = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("10.0.0.1")}, nil
	}
	var dials atomic.Int32
	client.dialPinnedAddress = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("unexpected dial")
	}

	_, err := client.GetNZB(context.Background(), "native-id")
	assertSafeCode(t, err, "newznab_redirect_dns_address_rejected")
	if dials.Load() != 0 {
		t.Fatalf("dial count=%d", dials.Load())
	}
	if scheduler.Snapshot("mixed").LastOutcome != indexers.OutcomeSystemFailure {
		t.Fatalf("scheduler outcome=%q", scheduler.Snapshot("mixed").LastOutcome)
	}
}

func TestGetNZBRedirectValidatesAndPinsEveryHopWithoutProxyOrReuse(t *testing.T) {
	var requests atomic.Int32
	target, roots := newNamedTLSServer(t, []string{"one.test", "two.test"}, func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Header.Get("Referer") != "" || request.URL.Query().Has("apikey") {
			t.Error("redirect hop leaked source metadata")
		}
		switch request.URL.Path {
		case "/first":
			writer.Header().Set("Location", "/second?ticket=two")
			writer.WriteHeader(http.StatusTemporaryRedirect)
		case "/second":
			if request.URL.Query().Get("ticket") != "two" {
				t.Error("relative redirect query was not replaced")
			}
			writer.Header().Set("Location", "https://two.test/final?ticket=three")
			writer.WriteHeader(http.StatusPermanentRedirect)
		case "/final":
			if request.URL.Query().Get("ticket") != "three" {
				t.Error("final redirect query was not preserved")
			}
			_, _ = writer.Write([]byte(redirectTestNZB))
		default:
			http.NotFound(writer, request)
		}
	}, nil)
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "https://one.test/first?ticket=one")
		writer.WriteHeader(http.StatusSeeOther)
	}))
	defer source.Close()

	var proxyCrossOriginCalls atomic.Int32
	proxy := func(request *http.Request) (*url.URL, error) {
		hostname := strings.TrimSuffix(strings.ToLower(request.URL.Hostname()), ".")
		if hostname == "one.test" || hostname == "two.test" {
			proxyCrossOriginCalls.Add(1)
			return url.Parse("http://127.0.0.1:1")
		}
		return nil, nil
	}
	client, _ := newRedirectTestClient(t, "multi-hop", source.URL, roots, proxy)
	var lock sync.Mutex
	resolved := map[string]int{}
	client.resolveRedirectAddresses = func(_ context.Context, hostname string) ([]netip.Addr, error) {
		lock.Lock()
		resolved[hostname]++
		lock.Unlock()
		switch hostname {
		case "one.test":
			return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
		case "two.test":
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		default:
			return nil, errors.New("unexpected host")
		}
	}
	var dials atomic.Int32
	client.dialPinnedAddress = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "1.1.1.1:443" && address != "8.8.8.8:443" {
			t.Fatalf("unvalidated dial address=%q", address)
		}
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}

	if _, err := client.GetNZB(context.Background(), "native-id"); err != nil {
		t.Fatal(err)
	}
	lock.Lock()
	oneResolutions, twoResolutions := resolved["one.test"], resolved["two.test"]
	lock.Unlock()
	if oneResolutions != 2 || twoResolutions != 1 {
		t.Fatalf("per-hop resolutions one=%d two=%d", oneResolutions, twoResolutions)
	}
	if dials.Load() != 3 || requests.Load() != 3 {
		t.Fatalf("dials=%d requests=%d", dials.Load(), requests.Load())
	}
	if proxyCrossOriginCalls.Load() != 0 {
		t.Fatalf("pinned requests consulted proxy %d times", proxyCrossOriginCalls.Load())
	}
}

func TestGetNZBRedirectTLSVerificationUsesRedirectHostname(t *testing.T) {
	var sni atomic.Value
	var handlerRequests atomic.Int32
	target, roots := newNamedTLSServer(t, []string{"wrong.test"}, func(http.ResponseWriter, *http.Request) {
		handlerRequests.Add(1)
	}, func(name string) { sni.Store(name) })
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "https://download.test/private/file?ticket=redirect-secret")
		writer.WriteHeader(http.StatusFound)
	}))
	defer source.Close()
	client, _ := newRedirectTestClient(t, "tls-name", source.URL, roots, nil)
	client.resolveRedirectAddresses = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}
	client.dialPinnedAddress = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}

	_, err := client.GetNZB(context.Background(), "native-id")
	assertSafeCode(t, err, "newznab_transport_failed")
	if got, _ := sni.Load().(string); got != "download.test" {
		t.Fatalf("TLS SNI=%q", got)
	}
	if handlerRequests.Load() != 0 {
		t.Fatalf("handler requests=%d after certificate mismatch", handlerRequests.Load())
	}
	for _, secret := range []string{"source-api-key-fixture", "redirect-secret", "download.test", "/private/file"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("persistable error exposed %q: %v", secret, err)
		}
	}
}

func TestCrossOriginRedirectFailureIsRedactedForLoggingAndPersistence(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "https://download.test/private/release.nzb?ticket=redirect-secret")
		writer.WriteHeader(http.StatusFound)
	}))
	defer source.Close()
	client, scheduler := newRedirectTestClient(t, "redacted", source.URL, nil, nil)
	client.resolveRedirectAddresses = func(context.Context, string) ([]netip.Addr, error) {
		return nil, errors.New("resolver detail must remain private")
	}

	_, err := client.GetNZB(context.Background(), "native-id")
	assertSafeCode(t, err, "newznab_redirect_dns_resolution_rejected")
	if err.Error() != "Newznab request failed" {
		t.Fatalf("persistable error=%q", err.Error())
	}
	for _, private := range []string{"source-api-key-fixture", "redirect-secret", "download.test", "/private/release.nzb", source.URL} {
		if strings.Contains(err.Error(), private) {
			t.Fatalf("persistable error exposed private URL material: %v", err)
		}
	}
	if scheduler.Snapshot("redacted").LastOutcome != indexers.OutcomeSystemFailure {
		t.Fatalf("scheduler outcome=%q", scheduler.Snapshot("redacted").LastOutcome)
	}
}

func TestPinnedRedirectTransportRejectsInsecureTLSConfiguration(t *testing.T) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client := &Client{
		config:            Config{Timeout: time.Second},
		transport:         transport,
		dialPinnedAddress: defaultPinnedDialer(time.Second),
	}
	target, err := url.Parse("https://download.test/file.nzb")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.newPinnedRedirectTransport(target, []netip.Addr{netip.MustParseAddr("1.1.1.1")}); !errors.Is(err, ErrUnsafeRedirect) {
		t.Fatalf("error=%v", err)
	}
}

func TestRedirectConstructionDoesNotInheritQueryOrOriginHeaders(t *testing.T) {
	client := &Client{config: Config{APIKey: "source-api-key-fixture"}}
	previous, err := url.Parse("https://indexer.test/api?apikey=source-api-key-fixture&o=xml&t=get")
	if err != nil {
		t.Fatal(err)
	}
	response := &http.Response{Header: http.Header{"Location": []string{"/download/release.nzb"}}}
	target, err := client.redirectTarget(previous, response)
	if err != nil {
		t.Fatal(err)
	}
	if target.RawQuery != "" {
		t.Fatalf("inherited query=%q", target.RawQuery)
	}
	request, err := newNewznabRequest(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Referer", "X-Indexer-Key"} {
		if request.Header.Get(header) != "" {
			t.Fatalf("new redirect request contains %s", header)
		}
	}
	if request.Header.Get("Accept") == "" || request.Header.Get("User-Agent") != "MediaManager/1" {
		t.Fatalf("safe headers=%v", request.Header)
	}

	response.Header.Set("Location", "/download/release.nzb?ticket=one&apikey=source-api-key-fixture&keep=two")
	target, err = client.redirectTarget(previous, response)
	if err != nil {
		t.Fatal(err)
	}
	if target.RawQuery != "ticket=one&keep=two" {
		t.Fatalf("source credential was not removed exactly: query keys=%v", queryKeys(target.Query()))
	}
}

func TestRedirectSourceCredentialIsRemovedBeforeTheNextRequest(t *testing.T) {
	var redirectedRequests atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/download/release.nzb" {
			redirectedRequests.Add(1)
			if request.URL.Query().Has("apikey") || request.URL.Query().Get("ticket") != "one" || strings.Contains(request.URL.RawQuery, "source-api-key-fixture") {
				t.Errorf("redirect request retained source credentials: keys=%v", queryKeys(request.URL.Query()))
			}
			_, _ = writer.Write([]byte(redirectTestNZB))
			return
		}
		writer.Header().Set("Location", "/download/release.nzb?ticket=one&apikey=source-api-key-fixture")
		writer.WriteHeader(http.StatusFound)
	}))
	defer source.Close()
	client, _ := newRedirectTestClient(t, "credential", source.URL, nil, nil)

	if _, err := client.GetNZB(context.Background(), "native-id"); err != nil {
		t.Fatal(err)
	}
	if redirectedRequests.Load() != 1 {
		t.Fatalf("redirect requests=%d", redirectedRequests.Load())
	}
}

func TestExactOriginUsesSchemeNormalizedHostnameAndEffectivePort(t *testing.T) {
	parse := func(value string) requestOrigin {
		t.Helper()
		parsed, err := url.Parse(value)
		if err != nil {
			t.Fatal(err)
		}
		origin, err := exactOrigin(parsed)
		if err != nil {
			t.Fatal(err)
		}
		return origin
	}
	canonical := parse("https://EXAMPLE.test./path")
	if canonical != parse("https://example.test:443/other") {
		t.Fatal("normalized default HTTPS port did not match explicit port")
	}
	for _, different := range []string{"http://example.test:443/path", "https://example.test:444/path", "https://other.test/path"} {
		if canonical == parse(different) {
			t.Fatalf("origin incorrectly matched %s", different)
		}
	}
}

func TestCrossOriginRedirectTargetRejectsUnsafeURLFormsBeforeDNS(t *testing.T) {
	client := &Client{resolveRedirectAddresses: func(context.Context, string) ([]netip.Addr, error) {
		t.Fatal("unsafe target reached DNS resolution")
		return nil, nil
	}}
	for _, value := range []string{
		"http://download.test/file.nzb",
		"https://download.test:444/file.nzb",
		"https://user:password@download.test/file.nzb",
		"https://download.test/file.nzb#fragment",
		"https://127.0.0.1/file.nzb",
		"https://localhost/file.nzb",
	} {
		t.Run(value, func(t *testing.T) {
			target, err := url.Parse(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.resolveAndValidateRedirectTarget(context.Background(), target); !errors.Is(err, ErrUnsafeRedirect) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestGetNZBManualRedirectCountIsBounded(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		step := requests.Add(1)
		writer.Header().Set("Location", "/redirect/"+big.NewInt(int64(step)).String())
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client, _ := newRedirectTestClient(t, "bounded", server.URL, nil, nil)
	_, err := client.GetNZB(context.Background(), "native-id")
	assertSafeCode(t, err, "newznab_too_many_redirects")
	if requests.Load() != maxRedirects+1 {
		t.Fatalf("requests=%d want=%d", requests.Load(), maxRedirects+1)
	}
}

func TestRedirectedNZBStillUsesStrictBoundedValidation(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		limit    int64
		wantCode string
	}{
		{name: "strict parser", body: `<nzb></nzb>`, limit: 1024, wantCode: "newznab_invalid_nzb"},
		{name: "bounded body", body: strings.Repeat("x", 65), limit: 64, wantCode: "newznab_response_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, roots := newNamedTLSServer(t, []string{"download.test"}, func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte(test.body))
			}, nil)
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Location", "https://download.test/file.nzb")
				writer.WriteHeader(http.StatusFound)
			}))
			defer source.Close()
			client, scheduler := newRedirectTestClient(t, "validation", source.URL, roots, nil)
			client.nzbLimit = test.limit
			client.resolveRedirectAddresses = func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
			}
			client.dialPinnedAddress = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
			}
			_, err := client.GetNZB(context.Background(), "native-id")
			assertSafeCode(t, err, test.wantCode)
			if scheduler.Snapshot("validation").LastOutcome != indexers.OutcomeSystemFailure {
				t.Fatalf("scheduler outcome=%q", scheduler.Snapshot("validation").LastOutcome)
			}
		})
	}
}

func newRedirectTestClient(t *testing.T, indexerID, sourceURL string, roots *x509.CertPool, proxy func(*http.Request) (*url.URL, error)) (*Client, *indexers.Scheduler) {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	if proxy != nil {
		transport.Proxy = proxy
	} else {
		transport.Proxy = nil
	}
	scheduler := indexers.NewScheduler(1, nil)
	if err := scheduler.Configure(indexerID, indexers.Budget{FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IndexerID: indexerID, BaseURL: sourceURL, APIKey: "source-api-key-fixture", AllowHTTP: true, Timeout: 5 * time.Second}, scheduler, transport)
	if err != nil {
		t.Fatal(err)
	}
	return client, scheduler
}

func newNamedTLSServer(t *testing.T, names []string, handler http.HandlerFunc, observeSNI func(string)) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	certificate, roots := newTestCertificate(t, names)
	tlsConfig := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	if observeSNI != nil {
		tlsConfig.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			observeSNI(hello.ServerName)
			return nil, nil
		}
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = tlsConfig
	server.StartTLS()
	return server, roots
}

func newTestCertificate(t *testing.T, names []string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Media Manager redirect test CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return certificate, roots
}

func assertSafeCode(t *testing.T, err error, want string) {
	t.Helper()
	var coded interface{ SafeCode() string }
	if !errors.As(err, &coded) || coded.SafeCode() != want {
		t.Fatalf("error=%T %v safe_code=%q want=%q", err, err, safeCode(coded), want)
	}
}

func safeCode(coded interface{ SafeCode() string }) string {
	if coded == nil {
		return ""
	}
	return coded.SafeCode()
}

func queryKeys(values url.Values) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
