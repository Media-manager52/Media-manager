package newznab

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	commissioningMaxBodyBytes      = 2 << 20
	commissioningMaxHeaderBytes    = 1 << 20
	commissioningMaxHeaderCount    = 128
	commissioningMaxHeaderValBytes = 16 << 10
	commissioningMaxXMLTokens      = 250000
	commissioningMaxXMLDepth       = 64
	commissioningMaxXMLElements    = 20000
	commissioningMaxXMLAttrs       = 20000
	commissioningMaxXMLTextBytes   = 2 << 20
	commissioningMaxParams         = 16
)

var (
	ErrCommissioningGuardRejected   = errors.New("commissioning guard rejected request")
	ErrCommissioningUnsafeRedirect  = errors.New("commissioning redirect rejected")
	ErrCommissioningTransport       = errors.New("commissioning transport failed")
	ErrCommissioningBodyLimit       = errors.New("commissioning response exceeds bound")
	ErrCommissioningMalformedXML    = errors.New("commissioning malformed XML")
	ErrCommissioningTrailingXML     = errors.New("commissioning trailing XML rejected")
	ErrCommissioningUnsafeText      = errors.New("commissioning unsafe text rejected")
	ErrCommissioningSecretEcho      = errors.New("commissioning credential echo rejected")
	ErrCommissioningDNSPolicy       = errors.New("commissioning DNS policy rejected")
	ErrCommissioningTLSVerification = errors.New("commissioning TLS verification failed")
	ErrCommissioningContentEncoding = errors.New("commissioning content encoding rejected")
	ErrCommissioningMediaType       = errors.New("commissioning media type rejected")
)

type commissioningSafeError struct {
	code  string
	cause error
}

func (err commissioningSafeError) Error() string    { return err.cause.Error() }
func (err commissioningSafeError) Unwrap() error    { return err.cause }
func (err commissioningSafeError) SafeCode() string { return err.code }

func commissioningClassified(code string, cause error) error {
	return commissioningSafeError{code: code, cause: cause}
}

// CommissioningGuard is owned by the Newznab client package to avoid an
// import cycle: commissioningv2 already imports Newznab types. The executor
// adapts the pure exact-secret inspector and freshness checker to these
// callbacks. Callbacks return only allow or a closed safe failure and must
// not receive a logger nor return provider text.
type CommissioningGuard interface {
	// BeforeSend checks cancellation and freshness immediately before the
	// single HTTP transmission. It must return a closed error only.
	BeforeSend(ctx context.Context) error
	// InspectSecret receives bounded raw body bytes, response headers, and
	// complete reconstructed decoded XML attribute values and logical text
	// values. It returns a closed error only and never returns provider text.
	InspectSecret(raw []byte, headers http.Header, decodedAttrs [][]byte, decodedTexts [][]byte) error
	// InspectStructure validates bounded structural provider text. It returns
	// a closed error only.
	InspectStructure(raw []byte, headers http.Header) error
}

// CommissioningDestination constrains the initial origin. TestLoopback may be
// supplied only by the injected local-test harness, never provider data or a
// production bypass setting.
type CommissioningDestination struct {
	Resolve func(ctx context.Context, hostname string) ([]netip.Addr, error)
	Dial    func(ctx context.Context, network, address string) (net.Conn, error)
	Roots   *x509.CertPool
	// TestLoopback permits loopback dial addresses for local fake TLS
	// servers. It must remain false outside local tests.
	TestLoopback bool
	Timeout      time.Duration
}

// CommissioningProbe is the only request shape the guarded path accepts. It
// never accepts a URL, path template, arbitrary parameter map, provider
// expression, or regular expression.
type CommissioningProbe struct {
	Mode       string
	Params     []Param
	Categories []int
	Limit      int
	Extended   bool
}

// CommissioningResult carries only safety-checked material. No raw provider
// URL, header, cookie, or error text escapes through this shape.
type CommissioningResult struct {
	Body        []byte
	Header      http.Header
	BodySHA256  [sha256.Size]byte
	DecodedAttr [][]byte
	DecodedText [][]byte
}

func validateCommissioningProbe(probe CommissioningProbe) error {
	mode := strings.TrimSpace(probe.Mode)
	if mode != "caps" && mode != "search" && mode != "movie" && mode != "tvsearch" {
		return commissioningClassified("commissioning_invalid_probe", ErrCommissioningTransport)
	}
	if len(probe.Params) > commissioningMaxParams {
		return commissioningClassified("commissioning_invalid_probe", ErrCommissioningTransport)
	}
	for _, param := range probe.Params {
		if strings.TrimSpace(param.Name) == "" || len(param.Name) > 64 || len(param.Value) > 4096 {
			return commissioningClassified("commissioning_invalid_probe", ErrCommissioningTransport)
		}
		for _, r := range param.Name {
			if r <= 0x20 || r >= 0x7f {
				return commissioningClassified("commissioning_invalid_probe", ErrCommissioningTransport)
			}
		}
	}
	if len(probe.Categories) > 128 {
		return commissioningClassified("commissioning_invalid_probe", ErrCommissioningTransport)
	}
	previous := 0
	for _, id := range probe.Categories {
		if id <= 0 || id <= previous {
			return commissioningClassified("commissioning_invalid_probe", ErrCommissioningTransport)
		}
		previous = id
	}
	if probe.Limit < 0 || probe.Limit > 100 {
		return commissioningClassified("commissioning_invalid_probe", ErrCommissioningTransport)
	}
	return nil
}

func commissioningOrigin(target *url.URL) (requestOrigin, error) {
	return exactOrigin(target)
}

// FetchCommissioning performs exactly one guarded HTTP transmission with a
// fresh non-reusing HTTP/1-only transport. Redirects are forbidden, transport
// replay is forbidden, and the one-use invocation is consumed even on error.
func (client *Client) FetchCommissioning(ctx context.Context, probe CommissioningProbe, dest CommissioningDestination, guard CommissioningGuard, deadline time.Time) (CommissioningResult, error) {
	var zero CommissioningResult
	if err := validateCommissioningProbe(probe); err != nil {
		return zero, err
	}
	if guard == nil {
		return zero, commissioningClassified("commissioning_guard_missing", ErrCommissioningGuardRejected)
	}
	if deadline.IsZero() {
		return zero, commissioningClassified("commissioning_deadline_missing", ErrCommissioningGuardRejected)
	}
	now := time.Now()
	if !now.Before(deadline) {
		return zero, commissioningClassified("commissioning_deadline_expired", ErrCommissioningGuardRejected)
	}
	if err := guard.BeforeSend(ctx); err != nil {
		return zero, sanitizeGuardError(err)
	}
	endpoint := *client.base
	params := []Param{{Name: "t", Value: strings.TrimSpace(probe.Mode)}}
	params = append(params, probe.Params...)
	if len(probe.Categories) > 0 {
		parts := make([]string, 0, len(probe.Categories))
		for _, category := range probe.Categories {
			parts = append(parts, itoa(category))
		}
		params = append(params, Param{Name: "cat", Value: strings.Join(parts, ",")})
	}
	if probe.Limit > 0 {
		params = append(params, Param{Name: "limit", Value: itoa(probe.Limit)})
	}
	if probe.Extended {
		params = append(params, Param{Name: "extended", Value: "1"})
	}
	endpoint.RawQuery = encode(params, client.config.APIKey)
	origin, err := commissioningOrigin(&endpoint)
	if err != nil {
		return zero, commissioningClassified("commissioning_origin_rejected", ErrCommissioningDNSPolicy)
	}
	if strings.ToLower(origin.scheme) != "https" {
		return zero, commissioningClassified("commissioning_downgrade_rejected", ErrCommissioningTLSVerification)
	}
	resolve := dest.Resolve
	if resolve == nil {
		resolve = lookupRedirectAddresses
	}
	addresses, err := resolve(ctx, origin.hostname)
	if err != nil {
		return zero, commissioningClassified("commissioning_dns_rejected", ErrCommissioningDNSPolicy)
	}
	if len(addresses) == 0 || len(addresses) > maxRedirectAddresses {
		return zero, commissioningClassified("commissioning_dns_rejected", ErrCommissioningDNSPolicy)
	}
	validated := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		unmapped := address.Unmap()
		if dest.TestLoopback && unmapped.IsLoopback() {
			// Allowed only via injected local-test harness.
		} else if !publicInternetAddress(unmapped) {
			return zero, commissioningClassified("commissioning_address_rejected", ErrCommissioningDNSPolicy)
		}
		if _, exists := seen[unmapped]; exists {
			continue
		}
		seen[unmapped] = struct{}{}
		validated = append(validated, unmapped)
	}
	if len(validated) == 0 {
		return zero, commissioningClassified("commissioning_dns_rejected", ErrCommissioningDNSPolicy)
	}
	timeout := dest.Timeout
	if timeout <= 0 {
		timeout = client.config.Timeout
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return zero, commissioningClassified("commissioning_deadline_expired", ErrCommissioningGuardRejected)
	}
	if timeout > remaining {
		timeout = remaining
	}
	dial := dest.Dial
	if dial == nil {
		dial = defaultPinnedDialer(timeout)
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: origin.hostname,
		NextProtos: []string{"http/1.1"},
	}
	if dest.Roots != nil {
		tlsConfig.RootCAs = dest.Roots
	} else if base, ok := client.transport.(*http.Transport); ok && base.TLSClientConfig != nil {
		if base.TLSClientConfig.InsecureSkipVerify {
			return zero, commissioningClassified("commissioning_tls_rejected", ErrCommissioningTLSVerification)
		}
		tlsConfig.RootCAs = base.TLSClientConfig.RootCAs
		if base.TLSClientConfig.MinVersion > tlsConfig.MinVersion {
			tlsConfig.MinVersion = base.TLSClientConfig.MinVersion
		}
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		MaxIdleConns:           0,
		MaxIdleConnsPerHost:    -1,
		MaxConnsPerHost:        1,
		IdleConnTimeout:        0,
		TLSClientConfig:        tlsConfig,
		TLSHandshakeTimeout:    timeout,
		ResponseHeaderTimeout:  timeout,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: commissioningMaxHeaderBytes,
		ForceAttemptHTTP2:      false,
	}
	transport.DialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		hostname, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil || port != origin.port {
			return nil, ErrCommissioningDNSPolicy
		}
		normalized, normalizeErr := normalizeHostname(hostname)
		if normalizeErr != nil || normalized != origin.hostname {
			return nil, ErrCommissioningDNSPolicy
		}
		var lastErr error
		for _, validatedAddress := range validated {
			connection, dialErr := dial(dialCtx, network, net.JoinHostPort(validatedAddress.String(), origin.port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
			if dialCtx.Err() != nil {
				break
			}
		}
		if lastErr == nil {
			lastErr = ErrCommissioningDNSPolicy
		}
		return nil, lastErr
	}
	httpClient := &http.Client{Transport: transport, CheckRedirect: rejectAutomaticRedirect}
	defer transport.CloseIdleConnections()
	requestCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	request, err := newCommissioningRequest(requestCtx, &endpoint)
	if err != nil {
		return zero, commissioningClassified("commissioning_request_rejected", ErrCommissioningTransport)
	}
	if err := guard.BeforeSend(requestCtx); err != nil {
		return zero, sanitizeGuardError(err)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return zero, commissioningClassified("commissioning_transport_failed", ErrCommissioningTransport)
	}
	defer response.Body.Close()
	if isRedirectStatus(response.StatusCode) {
		location := response.Header.Get("Location")
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, redirectDrainLimit))
		rawLocation := []byte(location)
		headerMaterials := flattenHeaders(response.Header)
		if guardErr := guard.InspectSecret(rawLocation, response.Header, nil, nil); guardErr != nil {
			return zero, sanitizeGuardError(guardErr)
		}
		_ = headerMaterials
		return zero, commissioningClassified("commissioning_redirect_rejected", ErrCommissioningUnsafeRedirect)
	}
	if err := checkContentEncoding(response.Header); err != nil {
		return zero, err
	}
	if err := checkMediaType(response.Header); err != nil {
		return zero, err
	}
	if err := validateHeaderBounds(response.Header); err != nil {
		return zero, err
	}
	if guardErr := guard.InspectSecret(nil, response.Header, nil, nil); guardErr != nil {
		return zero, sanitizeGuardError(guardErr)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, commissioningMaxBodyBytes+1))
	if err != nil {
		return zero, commissioningClassified("commissioning_read_failed", ErrCommissioningTransport)
	}
	if int64(len(body)) > commissioningMaxBodyBytes {
		return zero, commissioningClassified("commissioning_body_limit", ErrCommissioningBodyLimit)
	}
	if !utf8.Valid(body) {
		return zero, commissioningClassified("commissioning_malformed", ErrCommissioningMalformedXML)
	}
	attrs, texts, err := strictSingleRootScan(body)
	if err != nil {
		return zero, err
	}
	if guardErr := guard.InspectSecret(body, response.Header, attrs, texts); guardErr != nil {
		return zero, sanitizeGuardError(guardErr)
	}
	if guardErr := guard.InspectStructure(body, response.Header); guardErr != nil {
		return zero, sanitizeGuardError(guardErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return zero, commissioningHTTPStatus(response.StatusCode)
	}
	digest := sha256.Sum256(body)
	return CommissioningResult{Body: append([]byte(nil), body...), Header: cloneHeader(response.Header), BodySHA256: digest, DecodedAttr: attrs, DecodedText: texts}, nil
}

func sanitizeGuardError(err error) error {
	var coded interface{ SafeCode() string }
	if errors.As(err, &coded) {
		return commissioningClassified(coded.SafeCode(), ErrCommissioningGuardRejected)
	}
	return commissioningClassified("commissioning_guard_rejected", ErrCommissioningGuardRejected)
}

func commissioningHTTPStatus(status int) error {
	if status == http.StatusTooManyRequests {
		return commissioningClassified("commissioning_rate_limited", errors.New("commissioning rate limited"))
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return commissioningClassified("commissioning_auth_failed", errors.New("commissioning authentication failed"))
	}
	if status < 100 || status > 599 {
		return commissioningClassified("commissioning_http_error", ErrCommissioningTransport)
	}
	return commissioningClassified(fmt.Sprintf("commissioning_http_%d", status), ErrCommissioningTransport)
}

func newCommissioningRequest(ctx context.Context, target *url.URL) (*http.Request, error) {
	if target == nil {
		return nil, ErrCommissioningTransport
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/xml")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "MediaManager/1")
	request.Header.Del("Referer")
	request.Header.Del("Cookie")
	request.Header.Del("Authorization")
	request.Header.Del("Proxy-Authorization")
	request.Close = true
	return request, nil
}

func checkContentEncoding(header http.Header) error {
	encoding := strings.TrimSpace(strings.ToLower(header.Get("Content-Encoding")))
	if encoding == "" || encoding == "identity" {
		return nil
	}
	return commissioningClassified("commissioning_content_encoding", ErrCommissioningContentEncoding)
}

func checkMediaType(header http.Header) error {
	contentType := strings.TrimSpace(strings.ToLower(header.Get("Content-Type")))
	if contentType == "" {
		return nil
	}
	if strings.Contains(contentType, "xml") || strings.Contains(contentType, "rss") || strings.Contains(contentType, "text/plain") || strings.Contains(contentType, "text/xml") || strings.Contains(contentType, "application/xml") {
		return nil
	}
	return commissioningClassified("commissioning_media_type", ErrCommissioningMediaType)
}

func validateHeaderBounds(header http.Header) error {
	if len(header) > commissioningMaxHeaderCount {
		return commissioningClassified("commissioning_header_limit", ErrCommissioningBodyLimit)
	}
	for name, values := range header {
		if len(name) > 256 {
			return commissioningClassified("commissioning_header_limit", ErrCommissioningBodyLimit)
		}
		for _, value := range values {
			if len(value) > commissioningMaxHeaderValBytes {
				return commissioningClassified("commissioning_header_limit", ErrCommissioningBodyLimit)
			}
		}
	}
	return nil
}

func flattenHeaders(header http.Header) [][]byte {
	result := make([][]byte, 0, len(header)*2)
	for name, values := range header {
		result = append(result, []byte(name))
		for _, value := range values {
			result = append(result, []byte(value))
		}
	}
	return result
}

func cloneHeader(header http.Header) http.Header {
	cloned := make(http.Header, len(header))
	for name, values := range header {
		copied := make([]string, len(values))
		copy(copied, values)
		cloned[name] = copied
	}
	return cloned
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var buffer [32]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		position--
		buffer[position] = '-'
	}
	return string(buffer[position:])
}

// strictSingleRootScan enforces exactly one expected root, consumes through
// EOF, rejects a second/trailing root or any non-whitespace trailing content,
// forbids DTD/external resources and unbounded entity expansion, enforces
// byte/depth/element/attribute/text bounds, and returns complete reconstructed
// logical text values and attribute values for exact-secret inspection so
// splitting a credential across adjacent character-data tokens cannot bypass
// inspection.
func strictSingleRootScan(body []byte) (attrs [][]byte, texts [][]byte, err error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	decoder.Entity = xml.HTMLEntity
	depth := 0
	tokens := 0
	elements := 0
	attributes := 0
	textBytes := 0
	rootSeen := false
	rootClosed := false
	rootName := ""
	var currentText strings.Builder
	var currentIsText bool
	flushText := func() {
		if currentIsText {
			value := currentText.String()
			textBytes += len(value)
			if textBytes > commissioningMaxXMLTextBytes {
				return
			}
			texts = append(texts, []byte(value))
			currentText.Reset()
			currentIsText = false
		}
	}
	for {
		token, tokenErr := decoder.Token()
		if tokenErr != nil {
			if errors.Is(tokenErr, io.EOF) {
				break
			}
			return nil, nil, commissioningClassified("commissioning_malformed", ErrCommissioningMalformedXML)
		}
		tokens++
		if tokens > commissioningMaxXMLTokens {
			return nil, nil, commissioningClassified("commissioning_xml_limit", ErrCommissioningBodyLimit)
		}
		switch typed := token.(type) {
		case xml.StartElement:
			flushText()
			elements++
			if elements > commissioningMaxXMLElements {
				return nil, nil, commissioningClassified("commissioning_xml_limit", ErrCommissioningBodyLimit)
			}
			if depth >= commissioningMaxXMLDepth {
				return nil, nil, commissioningClassified("commissioning_xml_limit", ErrCommissioningBodyLimit)
			}
			if !rootSeen {
				rootSeen = true
				rootName = strings.ToLower(typed.Name.Local)
				if rootName != "caps" && rootName != "rss" && rootName != "error" && rootName != "nzb" {
					return nil, nil, commissioningClassified("commissioning_malformed", ErrCommissioningMalformedXML)
				}
			} else if rootClosed {
				return nil, nil, commissioningClassified("commissioning_trailing_root", ErrCommissioningTrailingXML)
			}
			depth++
			for _, attribute := range typed.Attr {
				attributes++
				if attributes > commissioningMaxXMLAttrs {
					return nil, nil, commissioningClassified("commissioning_xml_limit", ErrCommissioningBodyLimit)
				}
				if len(attribute.Value) > commissioningMaxXMLTextBytes {
					return nil, nil, commissioningClassified("commissioning_xml_limit", ErrCommissioningBodyLimit)
				}
				attrs = append(attrs, []byte(attribute.Name.Local), []byte(attribute.Value))
			}
			currentIsText = true
		case xml.EndElement:
			flushText()
			depth--
			if depth < 0 {
				return nil, nil, commissioningClassified("commissioning_malformed", ErrCommissioningMalformedXML)
			}
			if rootSeen && depth == 0 {
				if rootClosed {
					return nil, nil, commissioningClassified("commissioning_trailing_root", ErrCommissioningTrailingXML)
				}
				rootClosed = true
			}
			currentIsText = true
		case xml.CharData:
			if !rootSeen {
				if strings.TrimSpace(string(typed)) != "" {
					return nil, nil, commissioningClassified("commissioning_malformed", ErrCommissioningMalformedXML)
				}
				continue
			}
			if rootClosed {
				if strings.TrimSpace(string(typed)) != "" {
					return nil, nil, commissioningClassified("commissioning_trailing_root", ErrCommissioningTrailingXML)
				}
				continue
			}
			if len(typed)+currentText.Len() > commissioningMaxXMLTextBytes && currentText.Len()+len(typed) > commissioningMaxXMLTextBytes {
				return nil, nil, commissioningClassified("commissioning_xml_limit", ErrCommissioningBodyLimit)
			}
			_, _ = currentText.Write(typed)
			currentIsText = true
		case xml.Comment:
			flushText()
			currentIsText = true
		case xml.ProcInst:
			flushText()
			target := strings.ToLower(strings.TrimSpace(typed.Target))
			if target == "doctype" {
				return nil, nil, commissioningClassified("commissioning_malformed", ErrCommissioningMalformedXML)
			}
			currentIsText = true
		case xml.Directive:
			return nil, nil, commissioningClassified("commissioning_malformed", ErrCommissioningMalformedXML)
		}
	}
	flushText()
	if !rootSeen || !rootClosed || depth != 0 {
		return nil, nil, commissioningClassified("commissioning_malformed", ErrCommissioningMalformedXML)
	}
	if textBytes > commissioningMaxXMLTextBytes {
		return nil, nil, commissioningClassified("commissioning_xml_limit", ErrCommissioningBodyLimit)
	}
	return attrs, texts, nil
}

// StrictCommissioningCAPS parses only through the guarded strict path. Legacy
// ParseCaps remains unchanged for production compatibility.
func StrictCommissioningCAPS(body []byte) (CAPSModeObservation, CAPSModeObservation, CAPSModeObservation, error) {
	var empty CAPSModeObservation
	attrs, texts, err := strictSingleRootScan(body)
	if err != nil {
		return empty, empty, empty, err
	}
	_ = attrs
	_ = texts
	snapshot, err := ParseCaps(body, time.Now().UTC())
	if err != nil {
		return empty, empty, empty, commissioningClassified("commissioning_malformed", err)
	}
	if len(snapshot.ModeObservations) != 3 {
		return empty, empty, empty, commissioningClassified("commissioning_malformed", ErrCommissioningMalformedXML)
	}
	return snapshot.ModeObservations[0], snapshot.ModeObservations[1], snapshot.ModeObservations[2], nil
}
