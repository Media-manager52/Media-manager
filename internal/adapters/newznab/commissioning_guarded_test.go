package newznab

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
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

type allowGuard struct {
	beforeSend func(ctx context.Context) error
	inspect    func(raw []byte, headers http.Header, attrs [][]byte, texts [][]byte) error
	structure  func(raw []byte, headers http.Header) error
}

func (guard allowGuard) BeforeSend(ctx context.Context) error {
	if guard.beforeSend != nil {
		return guard.beforeSend(ctx)
	}
	return nil
}

func (guard allowGuard) InspectSecret(raw []byte, headers http.Header, attrs [][]byte, texts [][]byte) error {
	if guard.inspect != nil {
		return guard.inspect(raw, headers, attrs, texts)
	}
	return nil
}

func (guard allowGuard) InspectStructure(raw []byte, headers http.Header) error {
	if guard.structure != nil {
		return guard.structure(raw, headers)
	}
	return nil
}

func newLocalCommissioningClient(t *testing.T, indexerID, baseURL string, transport http.RoundTripper) (*Client, *indexers.Scheduler) {
	t.Helper()
	scheduler := indexers.NewScheduler(1, nil)
	if err := scheduler.Configure(indexerID, indexers.Budget{MinimumInterval: 3 * time.Second, FailureBackoff: time.Second, MaximumBackoff: time.Minute}); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{IndexerID: indexerID, BaseURL: baseURL, APIKey: "local-test-key", Timeout: 5 * time.Second}, scheduler, transport)
	if err != nil {
		t.Fatal(err)
	}
	return client, scheduler
}

func localLoopbackDest(t *testing.T, server *httptest.Server, dial func(ctx context.Context, network, address string) (net.Conn, error)) CommissioningDestination {
	t.Helper()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := serverURL.Hostname()
	roots := nilServerRoots(server)
	return CommissioningDestination{
		Resolve: func(_ context.Context, hostname string) ([]netip.Addr, error) {
			if hostname != strings.ToLower(host) {
				return nil, errors.New("unexpected hostname")
			}
			addr, err := netip.ParseAddr("127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			return []netip.Addr{addr}, nil
		},
		Dial:         dial,
		Roots:        roots,
		TestLoopback: true,
		Timeout:      5 * time.Second,
	}
}

func nilServerRoots(server *httptest.Server) *x509.CertPool {
	if server.Certificate() == nil {
		return nil
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	return roots
}

func TestCommissioningGuardedTransportSendsOnceOverFreshHTTP1Transport(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.ProtoMajor != 1 {
			t.Errorf("protocol=%q, want HTTP/1", request.Proto)
		}
		if request.Header.Get("Referer") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("Authorization") != "" {
			t.Errorf("request leaked origin headers: %v", request.Header)
		}
		if encoding := request.Header.Get("Accept-Encoding"); strings.Contains(strings.ToLower(encoding), "gzip") {
			t.Errorf("Accept-Encoding=%q, want identity", encoding)
		}
		if request.URL.Query().Get("t") != "caps" || request.URL.Query().Get("o") != "xml" {
			t.Errorf("query=%q", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte(`<caps><server title="local" version="1"/><limits default="20" max="100"/><registration available="yes" open="no"/><searching><search available="yes" supportedParams="q"/></searching></caps>`))
	}))
	defer server.Close()
	client, _ := newLocalCommissioningClient(t, "commission-once", server.URL, server.Client().Transport)
	dest := localLoopbackDest(t, server, func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			t.Fatalf("dial address=%q", address)
		}
		if host != "127.0.0.1" {
			t.Fatalf("unvalidated dial host=%q", host)
		}
		serverURL, _ := url.Parse(server.URL)
		_, serverPort, _ := net.SplitHostPort(serverURL.Host)
		if port != serverPort {
			t.Fatalf("dial port=%q, want %q", port, serverPort)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	// Rely only on the local fake TLS server certificate pool supplied
	// through dest.Roots, as the guarded production design intends.
	pinned, err := New(Config{IndexerID: "commission-once", BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, indexers.NewScheduler(1, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = client
	result, err := pinned.fetchCommissioningRawForTest(context.Background(), CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Body) == 0 || requests.Load() != 1 {
		t.Fatalf("requests=%d body=%d", requests.Load(), len(result.Body))
	}
}

func TestCommissioningGuardedTransportRejectsEveryRedirectWithoutSecondRequest(t *testing.T) {
	t.Parallel()
	var second atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/second" {
			second.Add(1)
		}
		writer.Header().Set("Location", "/second")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	base := server.Client().Transport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client, err := New(Config{IndexerID: "commission-redirect", BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, indexers.NewScheduler(1, nil), base)
	if err != nil {
		t.Fatal(err)
	}
	dest := localLoopbackDest(t, server, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	_, err = client.fetchCommissioningRawForTest(context.Background(), CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second))
	var coded interface{ SafeCode() string }
	if !errors.As(err, &coded) || coded.SafeCode() != "commissioning_redirect_rejected" {
		t.Fatalf("error=%v", err)
	}
	if second.Load() != 0 {
		t.Fatalf("redirect target requests=%d, want 0", second.Load())
	}
}

func TestCommissioningGuardedTransportRejectsPrivateMixedDNSAndProxyEnv(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("request must not be sent")
	}))
	defer server.Close()
	base := server.Client().Transport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client, err := New(Config{IndexerID: "commission-dns", BaseURL: server.URL, APIKey: "local-test-key", Timeout: 5 * time.Second}, indexers.NewScheduler(1, nil), base)
	if err != nil {
		t.Fatal(err)
	}
	dest := CommissioningDestination{
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("10.0.0.1")}, nil
		},
		Dial: func(context.Context, string, string) (net.Conn, error) {
			t.Error("dial must not occur after mixed DNS answers")
			return nil, errors.New("unexpected dial")
		},
		TestLoopback: false,
		Timeout:      5 * time.Second,
	}
	_, err = client.fetchCommissioningRawForTest(context.Background(), CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second))
	var coded interface{ SafeCode() string }
	if !errors.As(err, &coded) || coded.SafeCode() != "commissioning_address_rejected" {
		t.Fatalf("error=%v", err)
	}
}

func TestCommissioningGuardedTransportRejectsDowngradeAndEnforcesIdentityEncoding(t *testing.T) {
	t.Parallel()
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("plain HTTP must not be contacted")
	}))
	defer plain.Close()
	client, err := New(Config{IndexerID: "commission-downgrade", BaseURL: plain.URL, APIKey: "k", AllowHTTP: true, Timeout: 5 * time.Second}, indexers.NewScheduler(1, nil), plain.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.fetchCommissioningRawForTest(context.Background(), CommissioningProbe{Mode: "caps"}, CommissioningDestination{TestLoopback: true, Timeout: time.Second}, allowGuard{}, time.Now().Add(10*time.Second))
	var coded interface{ SafeCode() string }
	if !errors.As(err, &coded) || coded.SafeCode() != "commissioning_downgrade_rejected" {
		t.Fatalf("downgrade error=%v", err)
	}

	compressed := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/xml")
		writer.Header().Set("Content-Encoding", "gzip")
		_, _ = writer.Write([]byte(`<caps/>`))
	}))
	defer compressed.Close()
	base := compressed.Client().Transport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	compressedClient, err := New(Config{IndexerID: "commission-encoding", BaseURL: compressed.URL, APIKey: "k", Timeout: 5 * time.Second}, indexers.NewScheduler(1, nil), base)
	if err != nil {
		t.Fatal(err)
	}
	serverURL, _ := url.Parse(compressed.URL)
	_ = serverURL
	dest := localLoopbackDest(t, compressed, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, compressed.Listener.Addr().String())
	})
	_, err = compressedClient.fetchCommissioningRawForTest(context.Background(), CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second))
	if !errors.As(err, &coded) || coded.SafeCode() != "commissioning_content_encoding" {
		t.Fatalf("encoding error=%v", err)
	}
}

func TestCommissioningStrictXMLRejectsTrailingRootAndNonWhitespace(t *testing.T) {
	t.Parallel()
	valid := `<caps><searching><search available="yes" supportedParams="q"/></searching></caps>`
	if _, _, err := strictSingleRootScan([]byte(valid)); err != nil {
		t.Fatalf("valid CAPS rejected: %v", err)
	}
	for _, body := range []string{
		valid + `<caps/>`,
		valid + `{"trailing":"json"}`,
		valid + `tail`,
		`<caps/> trailing`,
		`<!DOCTYPE caps><caps/>`,
		"<caps>\x00</caps>",
	} {
		_, _, err := strictSingleRootScan([]byte(body))
		var coded interface{ SafeCode() string }
		if !errors.As(err, &coded) {
			t.Fatalf("body accepted without closed code: %q", body)
		}
	}
	oversized := make([]byte, commissioningMaxBodyBytes+2)
	for index := range oversized {
		oversized[index] = 'x'
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write(oversized)
	}))
	defer server.Close()
	base := server.Client().Transport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client, err := New(Config{IndexerID: "commission-limit", BaseURL: server.URL, APIKey: "k", Timeout: 5 * time.Second}, indexers.NewScheduler(1, nil), base)
	if err != nil {
		t.Fatal(err)
	}
	dest := localLoopbackDest(t, server, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	_, err = client.fetchCommissioningRawForTest(context.Background(), CommissioningProbe{Mode: "caps"}, dest, allowGuard{}, time.Now().Add(10*time.Second))
	var coded interface{ SafeCode() string }
	if !errors.As(err, &coded) || coded.SafeCode() != "commissioning_body_limit" {
		t.Fatalf("limit error=%v", err)
	}
}

func TestCommissioningGuardInspectsSecretBeforeDigestAndLeaksNothing(t *testing.T) {
	t.Parallel()
	secret := "local-secret-before-digest"
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/xml")
		writer.Header().Set("X-Echo", secret)
		_, _ = writer.Write([]byte(`<caps><server title="` + secret + `"/></caps>`))
	}))
	defer server.Close()
	base := server.Client().Transport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	client, err := New(Config{IndexerID: "commission-echo", BaseURL: server.URL, APIKey: "k", Timeout: 5 * time.Second}, indexers.NewScheduler(1, nil), base)
	if err != nil {
		t.Fatal(err)
	}
	dest := localLoopbackDest(t, server, func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	echo := commissioningClassified("credential.echo", ErrCommissioningSecretEcho)
	guard := allowGuard{inspect: func(raw []byte, headers http.Header, attrs [][]byte, texts [][]byte) error {
		if len(raw) == 0 && len(headers) > 0 {
			for _, values := range headers {
				for _, value := range values {
					if strings.Contains(value, secret) {
						return echo
					}
				}
			}
			return nil
		}
		combined := string(raw)
		for _, attr := range attrs {
			combined += string(attr)
		}
		for _, text := range texts {
			combined += string(text)
		}
		if strings.Contains(combined, secret) {
			return echo
		}
		return nil
	}}
	_, err = client.fetchCommissioningRawForTest(context.Background(), CommissioningProbe{Mode: "caps"}, dest, guard, time.Now().Add(10*time.Second))
	var coded interface{ SafeCode() string }
	if !errors.As(err, &coded) || coded.SafeCode() != "credential.echo" {
		t.Fatalf("echo error=%v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("closed error leaked the secret")
	}
}

func TestCommissioningTransportErrorsAreClosedAndSanitized(t *testing.T) {
	t.Parallel()
	client, err := New(Config{IndexerID: "commission-closed", BaseURL: "https://commissioning.invalid/api", APIKey: "super-secret-key", Timeout: time.Second}, indexers.NewScheduler(1, nil), roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial https://commissioning.invalid/api?t=caps&apikey=super-secret-key: connection refused")
	}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.fetchCommissioningRawForTest(context.Background(), CommissioningProbe{Mode: "caps"}, CommissioningDestination{
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
		},
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("unreachable")
		},
		Timeout: time.Second,
	}, allowGuard{}, time.Now().Add(10*time.Second))
	if err == nil {
		t.Fatal("expected transport error")
	}
	// 203.0.113.9 is documentation space and must fail address policy before
	// any dial error text can surface.
	for _, leaked := range []string{"super-secret-key", "commissioning.invalid", "connection refused", "unreachable"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("transport error leaked %q: %v", leaked, err)
		}
	}
	var coded interface{ SafeCode() string }
	if !errors.As(err, &coded) || coded.SafeCode() == "" {
		t.Fatalf("transport error has no closed code: %v", err)
	}
}
