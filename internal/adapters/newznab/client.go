package newznab

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	config    Config
	base      *url.URL
	http      *http.Client
	scheduler *indexers.Scheduler
	bodyLimit int64
	nzbLimit  int64
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
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && !(config.AllowHTTP && parsed.Scheme == "http")) {
		return nil, ErrInvalidEndpoint
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{config: config, base: parsed, scheduler: scheduler, http: &http.Client{Transport: transport, Timeout: config.Timeout, CheckRedirect: safeRedirect}, bodyLimit: defaultResponseLimit, nzbLimit: defaultNZBLimit}, nil
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
	return client.requestGuarded(ctx, indexers.WorkloadRetrieval, []Param{{Name: "t", Value: "get"}, {Name: "id", Value: nativeID}}, client.nzbLimit, guard, func(body []byte) (indexers.Outcome, error) {
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
	return client.requestGuarded(ctx, workload, params, limit, nil, validate)
}

func (client *Client) requestGuarded(ctx context.Context, workload indexers.Workload, params []Param, limit int64, guard func(context.Context) error, validate responseValidator) ([]byte, error) {
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

	endpoint := *client.base
	endpoint.RawQuery = encode(params, client.config.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		recorded = true
		_ = permit.Record(context.Background(), indexers.OutcomeSystemFailure, nil)
		return nil, classified("newznab_request_build_failed", errors.New("could not construct Newznab request"))
	}
	req.Header.Set("Accept", "application/xml, application/rss+xml, text/plain;q=0.8")
	req.Header.Set("User-Agent", "MediaManager/1")
	response, err := client.http.Do(req)
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
		}
		return nil, classified(code, errors.New("Newznab request failed"))
	}
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
	case status >= 400:
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

func safeRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return ErrTooManyRedirects
	}
	if len(via) > 0 && !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
		return ErrCrossHostRedirect
	}
	if req.URL.Scheme != "https" && via[0].URL.Scheme == "https" {
		return ErrTLSDowngrade
	}
	return nil
}
