package newznab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
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
	maxRedirects         = 2
	maxRedirectAddresses = 16
	redirectDrainLimit   = int64(8 << 10)
	maxNZBFiles          = 10_000
	maxNZBSegments       = 1_000_000
	maxNZBMessageIDBytes = 1_024
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

type NZBPackage struct {
	Body     []byte
	Identity [sha256.Size]byte
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
	ErrRedirectURLPolicy = errors.New("redirect URL policy rejected")
	ErrRedirectDNS       = errors.New("redirect DNS resolution rejected")
	ErrRedirectAddress   = errors.New("redirect DNS address rejected")
	ErrRedirectTransport = errors.New("redirect pinned transport rejected")
)

func unsafeRedirect(reason error) error {
	return errors.Join(ErrUnsafeRedirect, reason)
}

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
		observations = sanitizeObservationsWithCredential(parsed, client.config.APIKey)
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
		observations = sanitizeObservationsWithCredential(parsed, client.config.APIKey)
		if len(parsed) == 0 {
			return indexers.OutcomeNoResults, nil
		}
		return indexers.OutcomeSuccess, nil
	})
	return observations, err
}

func sanitizeObservationsWithCredential(observations []releases.Observation, credential string) []releases.Observation {
	result := make([]releases.Observation, len(observations))
	for index, observation := range observations {
		result[index] = SanitizeObservationWithCredential(observation, credential)
	}
	return result
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
	pack, err := client.GetNZBPackageGuarded(ctx, nativeID, guard)
	return pack.Body, err
}

func (client *Client) GetNZBPackageGuarded(ctx context.Context, nativeID string, guard func(context.Context) error) (NZBPackage, error) {
	var identity [sha256.Size]byte
	body, err := client.requestGuarded(ctx, indexers.WorkloadRetrieval, []Param{{Name: "t", Value: "get"}, {Name: "id", Value: nativeID}}, client.nzbLimit, guard, true, func(body []byte) (indexers.Outcome, error) {
		if provider, ok := providerErrorDocument(body); ok {
			return validationFailureOutcome(provider), provider
		}
		parsed, parseErr := PackageIdentity(body)
		if parseErr != nil {
			return indexers.OutcomeSystemFailure, classified("newznab_invalid_nzb", ErrInvalidNZB)
		}
		identity = parsed
		return indexers.OutcomeSuccess, nil
	})
	if err != nil {
		return NZBPackage{}, err
	}
	return NZBPackage{Body: body, Identity: identity}, nil
}

func validateNZB(body []byte) error {
	_, err := PackageIdentity(body)
	return err
}

func PackageIdentity(body []byte) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	identity := sha256.New()
	_, _ = identity.Write([]byte("media-manager-nzb-package-v1\x00"))
	rootSeen, rootClosed := false, false
	depth, files, segments, tokens := 0, 0, 0, 0
	fileDepth, fileSegments := 0, 0
	segmentDepth := 0
	segmentBytes, segmentNumber := uint64(0), uint64(0)
	var messageID strings.Builder
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return zero, err
		}
		tokens++
		if tokens > 4_000_000 {
			return zero, ErrInvalidNZB
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if !rootSeen {
				if !strings.EqualFold(typed.Name.Local, "nzb") {
					return zero, ErrInvalidNZB
				}
				rootSeen = true
			} else if rootClosed {
				return zero, ErrInvalidNZB
			}
			if segmentDepth != 0 {
				return zero, ErrInvalidNZB
			}
			depth++
			if strings.EqualFold(typed.Name.Local, "file") {
				if fileDepth != 0 {
					return zero, ErrInvalidNZB
				}
				files++
				if files > maxNZBFiles {
					return zero, ErrInvalidNZB
				}
				fileDepth, fileSegments = depth, 0
				_, _ = identity.Write([]byte{0xf0})
			}
			if strings.EqualFold(typed.Name.Local, "segment") {
				if fileDepth == 0 || depth <= fileDepth {
					return zero, ErrInvalidNZB
				}
				segments++
				fileSegments++
				if segments > maxNZBSegments {
					return zero, ErrInvalidNZB
				}
				var bytesText, numberText string
				for _, attribute := range typed.Attr {
					switch strings.ToLower(attribute.Name.Local) {
					case "bytes":
						bytesText = strings.TrimSpace(attribute.Value)
					case "number":
						numberText = strings.TrimSpace(attribute.Value)
					}
				}
				segmentBytes, err = strconv.ParseUint(bytesText, 10, 63)
				if err != nil || segmentBytes == 0 {
					return zero, ErrInvalidNZB
				}
				segmentNumber, err = strconv.ParseUint(numberText, 10, 31)
				if err != nil || segmentNumber == 0 {
					return zero, ErrInvalidNZB
				}
				segmentDepth = depth
				messageID.Reset()
			}
		case xml.CharData:
			if segmentDepth != 0 {
				if messageID.Len()+len(typed) > maxNZBMessageIDBytes {
					return zero, ErrInvalidNZB
				}
				_, _ = messageID.Write(typed)
			}
		case xml.EndElement:
			if segmentDepth == depth && strings.EqualFold(typed.Name.Local, "segment") {
				message := strings.TrimSpace(messageID.String())
				if message == "" {
					return zero, ErrInvalidNZB
				}
				_, _ = identity.Write([]byte{0x51})
				writeNZBIdentityUint(identity, segmentNumber)
				writeNZBIdentityUint(identity, segmentBytes)
				writeNZBIdentityUint(identity, uint64(len(message)))
				_, _ = io.WriteString(identity, message)
				segmentDepth = 0
			}
			if fileDepth == depth && strings.EqualFold(typed.Name.Local, "file") {
				if fileSegments == 0 || segmentDepth != 0 {
					return zero, ErrInvalidNZB
				}
				_, _ = identity.Write([]byte{0x0f})
				fileDepth = 0
			}
			depth--
			if depth < 0 {
				return zero, ErrInvalidNZB
			}
			if rootSeen && depth == 0 {
				rootClosed = true
			}
		}
	}
	if !rootSeen || !rootClosed || depth != 0 || fileDepth != 0 || segmentDepth != 0 || files == 0 || segments == 0 {
		return zero, ErrInvalidNZB
	}
	var result [sha256.Size]byte
	copy(result[:], identity.Sum(nil))
	return result, nil
}

func writeNZBIdentityUint(writer io.Writer, value uint64) {
	var encoded [binary.MaxVarintLen64]byte
	length := binary.PutUvarint(encoded[:], value)
	_, _ = writer.Write(encoded[:length])
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
	var guardFailure error
	requestGuard := guard
	if guard != nil {
		requestGuard = func(guardCtx context.Context) error {
			if err := guard(guardCtx); err != nil {
				guardFailure = err
				return err
			}
			return nil
		}
	}
	response, closeResponseTransport, redirected, err := client.doWithManualRedirects(requestCtx, req, allowCrossOriginRedirect, requestGuard)
	if guardFailure != nil {
		recorded = true
		_ = permit.Record(context.Background(), indexers.OutcomeCancelled, nil)
		return nil, guardFailure
	}
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
		case errors.Is(err, ErrRedirectURLPolicy):
			code = "newznab_redirect_url_policy_rejected"
		case errors.Is(err, ErrRedirectDNS):
			code = "newznab_redirect_dns_resolution_rejected"
		case errors.Is(err, ErrRedirectAddress):
			code = "newznab_redirect_dns_address_rejected"
		case errors.Is(err, ErrRedirectTransport):
			code = "newznab_redirect_pinned_transport_rejected"
		case errors.Is(err, ErrUnsafeRedirect):
			code = "newznab_unsafe_redirect_target_rejected"
		}
		return nil, classified(code, errors.New("Newznab request failed"))
	}
	defer closeResponseTransport()
	defer response.Body.Close()
	outcome := classify(response.StatusCode)
	if redirected && outcome == indexers.OutcomeAuthFailed {
		outcome = indexers.OutcomeSystemFailure
	}
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
		return finalizeProviderError(provider), true
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

func (client *Client) doWithManualRedirects(ctx context.Context, initial *http.Request, allowCrossOrigin bool, guard func(context.Context) error) (*http.Response, func(), bool, error) {
	if initial == nil || initial.URL == nil {
		return nil, func() {}, false, ErrUnsafeRedirect
	}
	originalOrigin, err := exactOrigin(initial.URL)
	if err != nil {
		return nil, func() {}, false, ErrUnsafeRedirect
	}
	request := initial
	for redirectCount := 0; ; redirectCount++ {
		currentOrigin, originErr := exactOrigin(request.URL)
		if originErr != nil {
			return nil, func() {}, redirectCount > 0, ErrUnsafeRedirect
		}
		requestClient := client.http
		closeTransport := func() {}
		if currentOrigin != originalOrigin {
			if !allowCrossOrigin {
				return nil, func() {}, redirectCount > 0, ErrCrossHostRedirect
			}
			addresses, resolveErr := client.resolveAndValidateRedirectTarget(ctx, request.URL)
			if resolveErr != nil {
				return nil, func() {}, redirectCount > 0, resolveErr
			}
			transport, transportErr := client.newPinnedRedirectTransport(request.URL, addresses)
			if transportErr != nil {
				return nil, func() {}, redirectCount > 0, transportErr
			}
			requestClient = &http.Client{Transport: transport, CheckRedirect: rejectAutomaticRedirect}
			closeTransport = transport.CloseIdleConnections
		}
		if guard != nil {
			if guardErr := guard(ctx); guardErr != nil {
				closeTransport()
				return nil, func() {}, redirectCount > 0, guardErr
			}
		}
		response, requestErr := requestClient.Do(request)
		if requestErr != nil {
			closeTransport()
			return nil, func() {}, redirectCount > 0, requestErr
		}
		if !isRedirectStatus(response.StatusCode) {
			return response, closeTransport, redirectCount > 0, nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, redirectDrainLimit))
		_ = response.Body.Close()
		closeTransport()
		if redirectCount >= maxRedirects {
			return nil, func() {}, true, ErrTooManyRedirects
		}
		next, redirectErr := client.redirectTarget(request.URL, response)
		if redirectErr != nil {
			return nil, func() {}, true, redirectErr
		}
		request, redirectErr = newNewznabRequest(ctx, next)
		if redirectErr != nil {
			return nil, func() {}, true, ErrUnsafeRedirect
		}
	}
}

func (client *Client) redirectTarget(previous *url.URL, response *http.Response) (*url.URL, error) {
	if previous == nil || response == nil {
		return nil, unsafeRedirect(ErrRedirectURLPolicy)
	}
	location := response.Header.Get("Location")
	if strings.TrimSpace(location) == "" || len(location) > 8192 {
		return nil, unsafeRedirect(ErrRedirectURLPolicy)
	}
	reference, err := url.Parse(location)
	if err != nil || reference.User != nil || reference.Fragment != "" {
		return nil, unsafeRedirect(ErrRedirectURLPolicy)
	}
	target := previous.ResolveReference(reference)
	if reference.RawQuery == "" && !reference.ForceQuery {
		target.RawQuery = ""
		target.ForceQuery = false
	}
	if target.User != nil || target.Fragment != "" || len(target.String()) > 8192 {
		return nil, unsafeRedirect(ErrRedirectURLPolicy)
	}
	if err := stripRedirectSourceCredential(target, client.config.APIKey); err != nil {
		return nil, unsafeRedirect(ErrRedirectURLPolicy)
	}
	previousOrigin, err := exactOrigin(previous)
	if err != nil {
		return nil, unsafeRedirect(ErrRedirectURLPolicy)
	}
	targetOrigin, err := exactOrigin(target)
	if err != nil {
		return nil, unsafeRedirect(ErrRedirectURLPolicy)
	}
	if targetOrigin != previousOrigin && targetOrigin.scheme == "http" && previousOrigin.scheme == "https" {
		return nil, ErrTLSDowngrade
	}
	return target, nil
}

func stripRedirectSourceCredential(target *url.URL, secret string) error {
	if target == nil || target.RawQuery == "" || secret == "" {
		return nil
	}
	components := strings.Split(target.RawQuery, "&")
	retained := make([]string, 0, len(components))
	for _, component := range components {
		name, value, _ := strings.Cut(component, "=")
		decodedName, nameErr := url.QueryUnescape(name)
		decodedValue, valueErr := url.QueryUnescape(value)
		if nameErr != nil || valueErr != nil {
			return ErrRedirectURLPolicy
		}
		if strings.Contains(component, secret) || strings.Contains(decodedName, secret) || strings.Contains(decodedValue, secret) {
			continue
		}
		retained = append(retained, component)
	}
	target.RawQuery = strings.Join(retained, "&")
	if target.RawQuery == "" {
		target.ForceQuery = false
	}
	return nil
}

func (client *Client) resolveAndValidateRedirectTarget(ctx context.Context, target *url.URL) ([]netip.Addr, error) {
	origin, err := exactOrigin(target)
	if err != nil || target == nil || len(target.String()) > 8192 || origin.scheme != "https" || origin.port != "443" || origin.hostname == "localhost" || net.ParseIP(origin.hostname) != nil || client.resolveRedirectAddresses == nil {
		return nil, unsafeRedirect(ErrRedirectURLPolicy)
	}
	addresses, err := client.resolveRedirectAddresses(ctx, origin.hostname)
	if err != nil {
		return nil, unsafeRedirect(ErrRedirectDNS)
	}
	if len(addresses) == 0 || len(addresses) > maxRedirectAddresses {
		return nil, unsafeRedirect(ErrRedirectAddress)
	}
	validated := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !publicInternetAddress(address) {
			return nil, unsafeRedirect(ErrRedirectAddress)
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		validated = append(validated, address)
	}
	if len(validated) == 0 {
		return nil, unsafeRedirect(ErrRedirectAddress)
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
		return nil, unsafeRedirect(ErrRedirectTransport)
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: origin.hostname,
		NextProtos: []string{"http/1.1"},
	}
	if base, ok := client.transport.(*http.Transport); ok && base.TLSClientConfig != nil {
		if base.TLSClientConfig.InsecureSkipVerify {
			return nil, unsafeRedirect(ErrRedirectTransport)
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
			return nil, unsafeRedirect(ErrRedirectTransport)
		}
		normalized, normalizeErr := normalizeHostname(hostname)
		if normalizeErr != nil || normalized != origin.hostname {
			return nil, unsafeRedirect(ErrRedirectTransport)
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
			lastErr = unsafeRedirect(ErrRedirectTransport)
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
