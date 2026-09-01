package metadataproviders

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/media-manager/media-manager/internal/catalog"
)

const defaultResponseLimit = int64(4 << 20)

var imdbID = regexp.MustCompile(`^tt[0-9]{7,12}$`)

var (
	ErrInvalidConfig       = errors.New("invalid metadata provider configuration")
	ErrIdentityUnavailable = errors.New("verified provider identity is unavailable")
	ErrIdentityConflict    = errors.New("metadata provider identity conflict")
	ErrProviderUnavailable = errors.New("metadata provider is unavailable")
	ErrResponseTooLarge    = errors.New("metadata provider response exceeds configured limit")
)

type Contribution struct {
	Metadata   *catalog.MediaMetadata
	Identities []catalog.ExternalIdentity
}

type Lookup interface {
	Lookup(context.Context, catalog.MediaKind, []catalog.ExternalIdentity, time.Time) (Contribution, error)
}

type clientConfig struct {
	base          *url.URL
	http          *http.Client
	responseLimit int64
}

func newClientConfig(baseURL string, allowHTTP bool, timeout time.Duration, responseLimit int64, transport http.RoundTripper) (clientConfig, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Scheme != "https" && !(allowHTTP && base.Scheme == "http") {
		return clientConfig{}, ErrInvalidConfig
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if timeout > 30*time.Second {
		return clientConfig{}, ErrInvalidConfig
	}
	if responseLimit <= 0 {
		responseLimit = defaultResponseLimit
	}
	if responseLimit > 8<<20 {
		return clientConfig{}, ErrInvalidConfig
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	return clientConfig{base: base, http: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, responseLimit: responseLimit}, nil
}

func (client clientConfig) endpoint(path string) url.URL {
	endpoint := *client.base
	endpoint.Path = strings.TrimSuffix(client.base.Path, "/") + path
	return endpoint
}

func (client clientConfig) decode(response *http.Response, target any) error {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrProviderUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, client.responseLimit+1))
	if err != nil {
		return ErrProviderUnavailable
	}
	if int64(len(body)) > client.responseLimit {
		return ErrResponseTooLarge
	}
	if err := json.Unmarshal(body, target); err != nil {
		return ErrProviderUnavailable
	}
	return nil
}

func verifiedIdentity(identities []catalog.ExternalIdentity, namespace string) string {
	for _, identity := range identities {
		if identity.State == catalog.IdentityVerified && strings.EqualFold(strings.TrimSpace(identity.Namespace), namespace) {
			return strings.TrimSpace(identity.Value)
		}
	}
	return ""
}

func positiveID(value string) bool {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return err == nil && parsed > 0
}

func identity(namespace, value, source string) catalog.ExternalIdentity {
	return catalog.ExternalIdentity{Namespace: namespace, Value: value, State: catalog.IdentityVerified, Source: source}
}

func validIdentity(namespace, value string) bool {
	value = strings.TrimSpace(value)
	if namespace == "imdb" {
		return imdbID.MatchString(value)
	}
	return (namespace == "tmdb" || namespace == "tvdb" || namespace == "simkl") && positiveID(value)
}

type stringID string

func (value *stringID) UnmarshalJSON(raw []byte) error {
	if strings.TrimSpace(string(raw)) == "null" {
		*value = ""
		return nil
	}
	var text string
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
	} else {
		var number json.Number
		if err := json.Unmarshal(raw, &number); err != nil {
			return err
		}
		text = number.String()
	}
	*value = stringID(strings.TrimSpace(text))
	return nil
}

func intPointer(value int64, maximum int64) *uint16 {
	if value <= 0 || value > maximum || value > 65_535 {
		return nil
	}
	result := uint16(value)
	return &result
}

func uint32Pointer(value int64) *uint32 {
	if value <= 0 || value > 1_000_000 {
		return nil
	}
	result := uint32(value)
	return &result
}

func firstDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 10 {
		candidate := value[:10]
		if _, err := time.Parse("2006-01-02", candidate); err == nil {
			return candidate
		}
	}
	return ""
}
