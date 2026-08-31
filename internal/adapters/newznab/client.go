package newznab

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/releases"
)

const (
	defaultResponseLimit = int64(16 << 20)
	defaultNZBLimit      = int64(128 << 20)
	maxRedirects         = 3
	maxRedirectAddresses = 16
	redirectDrainLimit   = int64(8 << 10)
)

type Param struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type SearchRequest struct {
	Mode       string  `json:"mode"`
	Params     []Param `json:"params"`
	Categories []int   `json:"categories,omitempty"`
	Limit      int     `json:"limit,omitempty"`
	Offset     int     `json:"offset,omitempty"`
	Extended   bool    `json:"extended"`
}

type Config struct {
	IndexerID string
	BaseURL   string
	APIKey    string
	AllowHTTP bool
	Timeout   time.Duration
}

type Client struct {
	config                   Config
	base                     *url.URL
	http                     *http.Client
	transport                http.RoundTripper
	scheduler                *indexers.Scheduler
	bodyLimit                int64
	nzbLimit                 int64
	resolveRedirectAddresses func(context.Context, string) ([]netip.Addr, error)
	dialPinnedAddress        func(context.Context, string, string) (net.Conn, error)
}

type HTTPError struct {
	StatusCode int
	RetryAfter string
}

func (err HTTPError) Error() string { return fmt.Sprintf("newznab returned HTTP %d", err.StatusCode) }
func (err HTTPError) SafeCode() string {
	if err.StatusCode < 100 || err.StatusCode > 599 {
		return "newznab_http_error"
	}
	return fmt.Sprintf("newznab_http_%d", err.StatusCode)
}

type safeError struct {
	code  string
	cause error
}

func (err safeError) Error() string    { return err.cause.Error() }
func (err safeError) Unwrap() error    { return err.cause }
func (err safeError) SafeCode() string { return err.code }

func classified(code string, cause error) error {
	return safeError{code: code, cause: cause}
}

var (
	ErrInvalidEndpoint   = errors.New("invalid Newznab endpoint")
	ErrResponseTooLarge  = errors.New("Newznab response exceeds the configured limit")
	ErrInvalidNZB        = errors.New("downloaded document is not NZB XML")
	ErrNoNFO             = errors.New("release has no NFO document")
	ErrSchedulerRequired = errors.New("indexer scheduler is required")
	ErrCrossHostRedirect = errors.New("cross-host redirect rejected")
	ErrTLSDowngrade      = errors.New("TLS downgrade rejected")
	ErrTooManyRedirects  = errors.New("too many redirects")
	ErrUnsafeRedirect    = errors.New("unsafe redirect target rejected")
)

type responseValidator func([]byte) (indexers.Outcome, error)

func New(config Config, scheduler *indexers.Scheduler, transport http.RoundTripper) (*Client, error) {
	if scheduler == nil {
		return nil, ErrSchedulerRequired
	}
	if strings.TrimSpace(config.IndexerID) == "" {
		return nil, indexers.ErrIndexerIDRequired
	}
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "https" && !(config.AllowHTTP && parsed.Scheme == "http")) {
		return nil, ErrInvalidEndpoint
	}
	if _, err := exactOrigin(parsed); err != nil {
		return nil, ErrInvalidEndpoint
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &Client{
		config:                   config,
		base:                     parsed,
		transport:                transport,
		scheduler:                scheduler,
		bodyLimit:                defaultResponseLimit,
		nzbLimit:                 defaultNZBLimit,
		resolveRedirectAddresses: lookupRedirectAddresses,
		dialPinnedAddress:        defaultPinnedDialer(config.Timeout),
	}
	client.http = &http.Client{Transport: transport, CheckRedirect: rejectAutomaticRedirect}
	return client, nil
}

func (client *Client) Search(ctx context.Context, request SearchRequest) ([]releases.Observation, error) {
	return client.search(ctx, indexers.WorkloadInteractive, request)
}

func (client *Client) CommissionSearch(ctx context.Context, request SearchRequest) ([]releases.Observation, error) {
	return client.search(ctx, indexers.WorkloadCommissioning, request)
}

func (client *Client) search(ctx context.Context, workload indexers.Workload, request SearchRequest) ([]releases.Observation, error) {
	mode := strings.TrimSpace(request.Mode)
	if mode == "" {
		mode = "search"
	}
	params := []Param{{Name: "t", Value: mode}}
	params = append(params, request.Params...)
	if len(request.Categories) > 0 {
		values := make([]string, 0, len(request.Categories))
		for _, category := range request.Categories {
			values = append(values, strconv.Itoa(category))
		}
		params = append(params, Param{Name: "cat", Value: strings.Join(values, ",")})
	}
	if request.Limit > 0 {
		params = append(params, Param{Name: "limit", Value: strconv.Itoa(request.Limit)})
	}
	if request.Offset > 0 {
		params = append(params, Param{Name: "offset", Value: strconv.Itoa(request.Offset)})
	}
	if request.Extended {
		params = append(params, Param{Name: "extended", Value: "1"})
	}
	var observations []releases.Observation
	_, err := client.request(ctx, workload, params, client.bodyLimit, func(body []byte) (indexers.Outcome, error) {
		parsed, parseErr := ParseSearch(body, client.config.IndexerID, mode, time.Now().UTC())
		if parseErr != nil {
			return validationFailureOutcome(parseErr), parseErr
		}
		observations = parsed
		if len(parsed) == 0 {
			return indexers.OutcomeNoResults, nil
		}
		return indexers.OutcomeSuccess, nil
	})
	return observations, err
}

func (client *Client) Caps(ctx context.Context) (CapsSnapshot, error) {
	var snapshot CapsSnapshot
	_, err := client.request(ctx, indexers.WorkloadCommissioning, []Param{{Name: "t", Value: "caps"}}, client.bodyLimit, func(body []byte) (indexers.Outcome, error) {
		parsed, parseErr := ParseCaps(body, time.Now().UTC())
		if parseErr != nil {
			return validationFailureOutcome(parseErr), parseErr
		}
		snapshot = parsed
		return indexers.OutcomeSuccess, nil
	})
	return snapshot, err
}

func (client *Client) Details(ctx context.Context, nativeID string) ([]releases.Observation, error) {
	var observations []releases.Observation
	_, err := client.request(ctx, indexers.WorkloadCommissioning, []Param{{Name: "t", Value: "details"}, {Name: "id", Value: nativeID}}, client.bodyLimit, func(body []byte) (indexers.Outcome, error) {
		parsed, parseErr := ParseSearch(body, client.config.IndexerID, "details", time.Now().UTC())
		if parseErr != nil {
			return validationFailureOutcome(parseErr), parseErr
		}
		observations = parsed
		if len(parsed) == 0 {
			return indexers.OutcomeNoResults, nil
		}
		return indexers.OutcomeSuccess, nil
	})
	return observations, err
}

func (client *Client) GetNFO(ctx context.Context, nativeID string) ([]byte, error) {
	return client.request(ctx, indexers.WorkloadCommissioning, []Param{{Name: "t", Value: "getnfo"}, {Name: "id", Value: nativeID}}, client.bodyLimit, func(body []byte) (indexers.Outcome, error) {
		if provider, ok := providerErrorDocument(body); ok {
			return validationFailureOutcome(provider), provider
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return indexers.OutcomeNoResults, ErrNoNFO
		}
		return indexers.OutcomeSuccess, nil
	})
}

func (client *Client) GetNZB(ctx context.Context, nativeID string) ([]byte, error) {
	return client.GetNZBGuarded(ctx, nativeID, nil)
}

func (client *Client) GetNZBGuarded(ctx context.Context, nativeID string, guard func(context.Context) error) ([]byte, error) {
	return client.requestGuarded(ctx, indexers.WorkloadRetrieval, []Param{{Name: "t", Value: "get"}, {Name: "id", Value: nativeID}}, client.nzbLimit, guard, true, func(body []byte) (indexers.Outcome, error) {
		if provider, ok := providerErrorDocument(body); ok {
			return validationFailureOutcome(provider), provider
		}
		if err := validateNZB(body); err != nil {
			return indexers.OutcomeSystemFailure, classified("newznab_invalid_nzb", ErrInvalidNZB)
		}
		return indexers.OutcomeSuccess, nil
	})
}

func validateNZB(body []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	rootSeen, rootClosed := false, false
	depth, files, segments, tokens := 0, 0, 0, 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		tokens++
		if tokens > 4_000_000 {
			return ErrInvalidNZB
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if !rootSeen {
				if !strings.EqualFold(typed.Name.Local, "nzb") {
					return ErrInvalidNZB
				}
				rootSeen = true
			} else if rootClosed {
				return ErrInvalidNZB
			}
			depth++
			if strings.EqualFold(typed.Name.Local, "file") {
				files++
			}
			if strings.EqualFold(typed.Name.Local, "segment") {
				segments++
			}
		case xml.EndElement:
			depth--
			if depth < 0 {
				return ErrInvalidNZB
			}
			if rootSeen && depth == 0 {
				rootClosed = true
			}
		}
	}
	if !rootSeen || !rootClosed || depth != 0 || files == 0 || segments == 0 {
		return ErrInvalidNZB
	}
	return nil
}

func (client *Client) request(ctx context.Context, workload indexers.Workload, params []Param, limit int64, validate responseValidator) ([]byte, error) {
	return client.requestGuarded(ctx, workload, params, limit, nil, false, validate)
}

func (client *Client) requestGuarded(ctx context.Context, workload indexers.Workload, params []Param, limit int64, guard func(context.Context) error, allowCrossOriginRedirect bool, validate responseValidator) ([]byte, error) {
	permit, err := client.scheduler.Acquire(ctx, client.config.IndexerID, workload)
	if err != nil {
		return nil, err
	}
	recorded := false
	defer func() {
		if !recorded {
			permit.Close()
		}
	}()
	if guard != nil {
		if err := guard(ctx); err != nil {
			recorded = true
			_ = permit.Record(context.Background(), indexers.OutcomeCancelled, nil)
			return nil, err
		}
	}
	requestCtx, cancel := context.WithTimeout(ctx, client.config.Timeout)
	defer cancel()

	endpoint := *client.base
	endpoint.RawQuery = encode(params, client.config.APIKey)
	req, err := newNewznabRequest(requestCtx, &endpoint)
	if err != nil {
		recorded = true
		_ = permit.Record(context.Background(), indexers.OutcomeSystemFailure, nil)
		return nil, classified("newznab_request_build_failed", errors.New("could not construct Newznab request"))
	}
	response, closeResponseTransport, err := client.doWithManualRedirects(requestCtx, req, allowCrossOriginRedirect)
	if err != nil {
		recorded = true
		_ = permit.Record(context.Background(), indexers.OutcomeSystemFailure, nil)
		code := "newznab_transport_failed"
		switch {
		case errors.Is(err, ErrCrossHostRedirect):
			code = "newznab_cross_host_redirect_rejected"
		case errors.Is(err, ErrTLSDowngrade):
			code = "newznab_tls_downgrade_rejected"
		case errors.Is(err, ErrTooManyRedirects):
			code = "newznab_too_many_redirects"
		case errors.Is(err, ErrUnsafeRedirect):
			code = "newznab_unsafe_redirect_target_rejected"
		}
		return nil, classified(code, errors.New("Newznab request failed"))
	}
	defer closeResponseTransport()
	defer response.Body.Close()
	outcome := classify(response.StatusCode)
	body, readErr := readBounded(response.Body, limit)
	if readErr != nil {
		outcome = indexers.OutcomeSystemFailure
	}
	var validationErr error
	if readErr == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		outcome, validationErr = validate(body)
	}
	recorded = true
	if recordErr := permit.Record(context.Background(), outcome, response.Header); recordErr != nil {
		return nil, fmt.Errorf("persist indexer pacing state: %w", recordErr)
	}
	if readErr != nil {
		if errors.Is(readErr, ErrResponseTooLarge) {
			return nil, classified("newznab_response_too_large", readErr)
		}
		return nil, classified("newznab_response_read_failed", errors.New("Newznab response could not be read"))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, HTTPError{StatusCode: response.StatusCode, RetryAfter: response.Header.Get("Retry-After")}
	}
	if validationErr != nil {
		return nil, validationErr
	}
	return body, nil
}

func validationFailureOutcome(err error) indexers.Outcome {
	var provider ProviderError
	if errors.As(err, &provider) && (provider.Code == "100" || provider.Code == "101" || provider.Code == "102") {
		return indexers.OutcomeAuthFailed
	}
	return indexers.OutcomeSystemFailure
}

func providerErrorDocument(body []byte) (ProviderError, bool) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	for {
		token, err := decoder.Token()
		if err != nil {
			return ProviderError{}, false
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if !strings.EqualFold(start.Name.Local, "error") {
			return ProviderError{}, false
		}
		provider := ProviderError{}
		for _, attribute := range start.Attr {
			switch strings.ToLower(attribute.Name.Local) {
			case "code":
				provider.Code = attribute.Value
			case "description":
				provider.Description = attribute.Value
			}
		}
		provider.Unsupported = provider.Code == "203" || strings.Contains(strings.ToLower(provider.Description), "no such function") || strings.Contains(strings.ToLower(provider.Description), "not supported")
		return provider, true
	}
}

func encode(params []Param, apiKey string) string {
	all := make([]Param, 0, len(params)+2)
	all = append(all, Param{Name: "apikey", Value: apiKey}, Param{Name: "o", Value: "xml"})
	all = append(all, params...)
	parts := make([]string, 0, len(all))
	for _, param := range all {
		if strings.TrimSpace(param.Name) == "" {
			continue
		}
		parts = append(parts, url.QueryEscape(param.Name)+"="+url.QueryEscape(param.Value))
	}
	return strings.Join(parts, "&")
}

func classify(status int) indexers.Outcome {
	switch {
	case status == http.StatusTooManyRequests:
		return indexers.OutcomeRateLimited
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return indexers.OutcomeAuthFailed
	case status < 200 || status >= 300:
		return indexers.OutcomeSystemFailure
	default:
		return indexers.OutcomeSuccess
	}
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

func rejectAutomaticRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

type requestOrigin struct {
	scheme   string
	hostname string
	port     string
}

func exactOrigin(target *url.URL) (requestOrigin, error) {
	if target == nil || target.User != nil || target.Fragment != "" || strings.TrimSpace(target.Host) == "" {
		return requestOrigin{}, ErrUnsafeRedirect
	}
	scheme := strings.ToLower(strings.TrimSpace(target.Scheme))
	if scheme != "http" && scheme != "https" {
		return requestOrigin{}, ErrUnsafeRedirect
	}
	hostname, err := normalizeHostname(target.Hostname())
	if err != nil {
		return requestOrigin{}, err
	}
	port := target.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return requestOrigin{}, ErrUnsafeRedirect
	}
	return requestOrigin{scheme: scheme, hostname: hostname, port: strconv.FormatUint(portNumber, 10)}, nil
}

func normalizeHostname(hostname string) (string, error) {
	hostname = strings.TrimRight(strings.ToLower(strings.TrimSpace(hostname)), ".")
	if hostname == "" {
		return "", ErrUnsafeRedirect
	}
	for _, character := range hostname {
		if character <= 0x20 || character >= 0x7f {
			return "", ErrUnsafeRedirect
		}
	}
	return hostname, nil
}

func newNewznabRequest(ctx context.Context, target *url.URL) (*http.Request, error) {
	if target == nil {
		return nil, ErrUnsafeRedirect
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/xml, application/rss+xml, text/plain;q=0.8")
	request.Header.Set("User-Agent", "MediaManager/1")
	request.Header.Del("Referer")
	return request, nil
}

func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func (client *Client) doWithManualRedirects(ctx context.Context, initial *http.Request, allowCrossOrigin bool) (*http.Response, func(), error) {
	if initial == nil || initial.URL == nil {
		return nil, func() {}, ErrUnsafeRedirect
	}
	originalOrigin, err := exactOrigin(initial.URL)
	if err != nil {
		return nil, func() {}, ErrUnsafeRedirect
	}
	request := initial
	for redirectCount := 0; ; redirectCount++ {
		currentOrigin, originErr := exactOrigin(request.URL)
		if originErr != nil {
			return nil, func() {}, ErrUnsafeRedirect
		}
		requestClient := client.http
		closeTransport := func() {}
		if currentOrigin != originalOrigin {
			if !allowCrossOrigin {
				return nil, func() {}, ErrCrossHostRedirect
			}
			addresses, resolveErr := client.resolveAndValidateRedirectTarget(ctx, request.URL)
			if resolveErr != nil {
				return nil, func() {}, resolveErr
			}
			transport, transportErr := client.newPinnedRedirectTransport(request.URL, addresses)
			if transportErr != nil {
				return nil, func() {}, transportErr
			}
			requestClient = &http.Client{Transport: transport, CheckRedirect: rejectAutomaticRedirect}
			closeTransport = transport.CloseIdleConnections
		}
		response, requestErr := requestClient.Do(request)
		if requestErr != nil {
			closeTransport()
			return nil, func() {}, requestErr
		}
		if !isRedirectStatus(response.StatusCode) {
			return response, closeTransport, nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, redirectDrainLimit))
		_ = response.Body.Close()
		closeTransport()
		if redirectCount >= maxRedirects {
			return nil, func() {}, ErrTooManyRedirects
		}
		next, redirectErr := client.redirectTarget(request.URL, response)
		if redirectErr != nil {
			return nil, func() {}, redirectErr
		}
		request, redirectErr = newNewznabRequest(ctx, next)
		if redirectErr != nil {
			return nil, func() {}, ErrUnsafeRedirect
		}
	}
}

func (client *Client) redirectTarget(previous *url.URL, response *http.Response) (*url.URL, error) {
	if previous == nil || response == nil {
		return nil, ErrUnsafeRedirect
	}
	location := response.Header.Get("Location")
	if strings.TrimSpace(location) == "" || len(location) > 8192 {
		return nil, ErrUnsafeRedirect
	}
	reference, err := url.Parse(location)
	if err != nil || reference.User != nil || reference.Fragment != "" {
		return nil, ErrUnsafeRedirect
	}
	target := previous.ResolveReference(reference)
	if reference.RawQuery == "" && !reference.ForceQuery {
		target.RawQuery = ""
		target.ForceQuery = false
	}
	if target.User != nil || target.Fragment != "" || len(target.String()) > 8192 {
		return nil, ErrUnsafeRedirect
	}
	if redirectContainsSecret(target, client.config.APIKey) {
		return nil, ErrUnsafeRedirect
	}
	previousOrigin, err := exactOrigin(previous)
	if err != nil {
		return nil, ErrUnsafeRedirect
	}
	targetOrigin, err := exactOrigin(target)
	if err != nil {
		return nil, ErrUnsafeRedirect
	}
	if targetOrigin != previousOrigin && targetOrigin.scheme == "http" && previousOrigin.scheme == "https" {
		return nil, ErrTLSDowngrade
	}
	return target, nil
}

func redirectContainsSecret(target *url.URL, secret string) bool {
	if target == nil || target.RawQuery == "" || secret == "" {
		return false
	}
	decoded, err := url.QueryUnescape(target.RawQuery)
	if err != nil {
		return true
	}
	if strings.Contains(target.RawQuery, secret) || strings.Contains(decoded, secret) {
		return true
	}
	values, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return true
	}
	for _, entries := range values {
		for _, entry := range entries {
			if subtle.ConstantTimeCompare([]byte(entry), []byte(secret)) == 1 {
				return true
			}
		}
	}
	return false
}

func (client *Client) resolveAndValidateRedirectTarget(ctx context.Context, target *url.URL) ([]netip.Addr, error) {
	origin, err := exactOrigin(target)
	if err != nil || target == nil || len(target.String()) > 8192 || origin.scheme != "https" || origin.port != "443" || origin.hostname == "localhost" || net.ParseIP(origin.hostname) != nil || client.resolveRedirectAddresses == nil {
		return nil, ErrUnsafeRedirect
	}
	addresses, err := client.resolveRedirectAddresses(ctx, origin.hostname)
	if err != nil || len(addresses) == 0 || len(addresses) > maxRedirectAddresses {
		return nil, ErrUnsafeRedirect
	}
	validated := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !publicInternetAddress(address) {
			return nil, ErrUnsafeRedirect
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		validated = append(validated, address)
	}
	if len(validated) == 0 {
		return nil, ErrUnsafeRedirect
	}
	return validated, nil
}

func lookupRedirectAddresses(ctx context.Context, hostname string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", hostname)
}

func defaultPinnedDialer(timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	return dialer.DialContext
}

func (client *Client) newPinnedRedirectTransport(target *url.URL, addresses []netip.Addr) (*http.Transport, error) {
	origin, err := exactOrigin(target)
	if err != nil || origin.scheme != "https" || origin.port != "443" || len(addresses) == 0 || client.dialPinnedAddress == nil {
		return nil, ErrUnsafeRedirect
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: origin.hostname,
		NextProtos: []string{"http/1.1"},
	}
	if base, ok := client.transport.(*http.Transport); ok && base.TLSClientConfig != nil {
		if base.TLSClientConfig.InsecureSkipVerify {
			return nil, ErrUnsafeRedirect
		}
		tlsConfig.RootCAs = base.TLSClientConfig.RootCAs
		if base.TLSClientConfig.MinVersion > tlsConfig.MinVersion {
			tlsConfig.MinVersion = base.TLSClientConfig.MinVersion
		}
	}
	dialPinned := client.dialPinnedAddress
	transport := &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		MaxIdleConns:           0,
		MaxIdleConnsPerHost:    -1,
		MaxConnsPerHost:        1,
		IdleConnTimeout:        0,
		TLSClientConfig:        tlsConfig,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  client.config.Timeout,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 1 << 20,
		ForceAttemptHTTP2:      false,
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		hostname, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil || port != origin.port {
			return nil, ErrUnsafeRedirect
		}
		normalized, normalizeErr := normalizeHostname(hostname)
		if normalizeErr != nil || normalized != origin.hostname {
			return nil, ErrUnsafeRedirect
		}
		var lastErr error
		for _, validatedAddress := range addresses {
			connection, dialErr := dialPinned(ctx, network, net.JoinHostPort(validatedAddress.String(), origin.port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
			if ctx.Err() != nil {
				break
			}
		}
		if lastErr == nil {
			lastErr = ErrUnsafeRedirect
		}
		return nil, lastErr
	}
	return transport, nil
}

var nonPublicInternetPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func publicInternetAddress(address netip.Addr) bool {
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicInternetPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
