package oldboys

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/adapters/newznab"
	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
	"github.com/media-manager/media-manager/internal/releases"
)

const Origin = "https://oldboys.pw"
const MovieCategory = 1
const SeriesCategory = 2
const MaxPage = 4
const searchLimit = 4 << 20
const nzbLimit = 128 << 20

var numericID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var danishToken = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])danish(?:$|[^a-z0-9])`)

func DanishTitle(title string) bool { return danishToken.MatchString(title) }
func ValidID(id string) bool        { return numericID.MatchString(id) }

type Error struct{ Code string }

func (err Error) Error() string    { return "OldBoys request failed (" + err.Code + ")" }
func (err Error) SafeCode() string { return err.Code }

type SearchRequest struct {
	Name           string
	Category, Page int
}
type Client struct {
	id          uuid.UUID
	credentials oldboyscredentials.Credentials
	scheduler   *indexers.Scheduler
	http        *http.Client
	guard       func(context.Context) error
}

// New accepts no configurable host. Injected transports are for local tests;
// the production transport uses direct IPv4 and rejects every redirect.
func New(id uuid.UUID, credentials oldboyscredentials.Credentials, scheduler *indexers.Scheduler, transport http.RoundTripper, guard func(context.Context) error) (*Client, error) {
	if id == (uuid.UUID{}) || credentials.APIToken == "" || credentials.PassKey == "" || scheduler == nil {
		return nil, Error{"oldboys_configuration_unavailable"}
	}
	if transport == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		tr.DisableKeepAlives = true
		tr.DialContext = func(ctx context.Context, _ string, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp4", address)
		}
		transport = tr
	}
	return &Client{id: id, credentials: credentials, scheduler: scheduler, guard: guard, http: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (client *Client) Search(ctx context.Context, request SearchRequest, workload indexers.Workload, guard func(context.Context) error) ([]releases.Observation, error) {
	observations, _, err := client.ProbeSearch(ctx, request, workload, guard)
	return observations, err
}

// ProbeSearch also reports the raw row count so a negative control cannot
// pass by filtering out malformed, foreign or miscategorised provider rows.
func (client *Client) ProbeSearch(ctx context.Context, request SearchRequest, workload indexers.Workload, guard func(context.Context) error) ([]releases.Observation, int, error) {
	if strings.TrimSpace(request.Name) == "" || len(request.Name) > 512 || (request.Category != MovieCategory && request.Category != SeriesCategory) || request.Page < 1 || request.Page > MaxPage {
		return nil, 0, Error{"oldboys_invalid_search"}
	}
	query := url.Values{"name": {request.Name}, "categories[]": {strconv.Itoa(request.Category)}, "page": {strconv.Itoa(request.Page)}}
	var observations []releases.Observation
	total := 0
	_, err := client.request(ctx, "/api/nzbs/filter?"+query.Encode(), workload, searchLimit, guard, func(body []byte) error {
		var parseErr error
		observations, total, parseErr = client.parse(body, request.Category)
		return parseErr
	})
	return observations, total, err
}

func (client *Client) GetNZBPackageGuarded(ctx context.Context, id string, guard func(context.Context) error) (newznab.NZBPackage, error) {
	if !ValidID(id) {
		return newznab.NZBPackage{}, Error{"oldboys_invalid_release_id"}
	}
	var identity [sha256.Size]byte
	// PID, not RID, authenticates this native download route. The credential is
	// built into this URL in memory only; no locator is saved with observations.
	body, err := client.request(ctx, "/nzbs/download/"+id+"."+url.PathEscape(client.credentials.PassKey), indexers.WorkloadRetrieval, nzbLimit, guard, func(body []byte) error {
		var parseErr error
		identity, parseErr = newznab.PackageIdentity(body)
		if parseErr != nil {
			return Error{"oldboys_invalid_nzb"}
		}
		return nil
	})
	if err != nil {
		return newznab.NZBPackage{}, err
	}
	return newznab.NZBPackage{Body: body, Identity: identity}, nil
}

func (client *Client) request(ctx context.Context, path string, workload indexers.Workload, limit int64, guard func(context.Context) error, validate func([]byte) error) (result []byte, err error) {
	permit, err := client.scheduler.Acquire(ctx, client.id.String(), workload)
	if err != nil {
		return nil, err
	}
	defer permit.Close()
	outcome := indexers.OutcomeSystemFailure
	var headers http.Header
	defer func() {
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if recordErr := permit.Record(recordCtx, outcome, headers); recordErr != nil {
			result = nil
			err = Error{"oldboys_scheduler_record_failed"}
		}
	}()
	if client.guard != nil {
		if err := client.guard(ctx); err != nil {
			outcome = indexers.OutcomeCancelled
			return nil, err
		}
	}
	if guard != nil {
		if err := guard(ctx); err != nil {
			outcome = indexers.OutcomeCancelled
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Origin+path, nil)
	if err != nil {
		return nil, Error{"oldboys_invalid_request"}
	}
	req.Header.Set("Authorization", "Bearer "+client.credentials.APIToken)
	req.Header.Set("User-Agent", "MediaManager/1")
	req.Header.Set("Accept", "application/json, application/x-nzb;q=0.9")
	response, err := client.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			outcome = indexers.OutcomeCancelled
		}
		return nil, Error{"oldboys_transport_failed"}
	}
	defer response.Body.Close()
	headers = response.Header
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case 401, 403:
			outcome = indexers.OutcomeAuthFailed
		case 429:
			outcome = indexers.OutcomeRateLimited
		}
		return nil, Error{fmt.Sprintf("oldboys_http_%d", response.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, Error{"oldboys_response_failed"}
	}
	if int64(len(body)) > limit {
		return nil, Error{"oldboys_response_too_large"}
	}
	if err := validate(body); err != nil {
		return nil, err
	}
	outcome = indexers.OutcomeSuccess
	return body, nil
}

func (client *Client) clean(value string) string {
	for _, secret := range []string{client.credentials.APIToken, client.credentials.PassKey, client.credentials.RSSKey} {
		if secret == "" {
			continue
		}
		for _, initial := range []string{secret, url.PathEscape(secret)} {
			variant := initial
			for depth := 0; depth < 4; depth++ {
				value = strings.ReplaceAll(value, variant, "[redacted]")
				variant = url.QueryEscape(variant)
			}
		}
	}
	return newznab.SanitizeProviderValue(value)
}

func scalar(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String()
	}
	return ""
}

func (client *Client) parse(body []byte, category int) ([]releases.Observation, int, error) {
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil || len(payload.Data) == 0 || string(payload.Data) == "null" {
		return nil, 0, Error{"oldboys_invalid_json"}
	}
	var rows []struct {
		ID         json.RawMessage            `json:"id"`
		Attributes map[string]json.RawMessage `json:"attributes"`
	}
	if json.Unmarshal(payload.Data, &rows) != nil || len(rows) > 25 {
		return nil, 0, Error{"oldboys_invalid_results"}
	}
	now := time.Now().UTC()
	hash := sha256.Sum256(body)
	result := make([]releases.Observation, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		id := scalar(row.ID)
		title := client.clean(scalar(row.Attributes["name"]))
		rowCategory, _ := strconv.Atoi(scalar(row.Attributes["category_id"]))
		if !ValidID(id) || title == "" || len(title) > 1024 || rowCategory != category || !DanishTitle(title) || seen[id] {
			continue
		}
		seen[id] = true
		observation := releases.Observation{ID: uuid.NewV7(), IndexerID: client.id, NativeGUID: id, Title: title, Operation: "oldboys_filter", ParseVersion: "oldboys-v1", BodyHash: hex.EncodeToString(hash[:]), ObservedAt: now}
		// Provider URLs, descriptions, user objects and arbitrary attributes are
		// deliberately excluded. Only bounded scalar evidence enters persistence.
		for _, name := range []string{"name", "category_id", "created_at", "resolution", "size", "imdb_id", "tmdb_id", "tvdb_id", "release_year"} {
			value := client.clean(scalar(row.Attributes[name]))
			if len(value) > 1024 {
				value = ""
			}
			observation.Fields = append(observation.Fields, releases.RawField{Name: name, Value: value, Ordinal: len(observation.Fields)})
		}
		for _, spec := range []struct{ upstream, fact string }{{"size", "size"}, {"imdb_id", "imdb_id"}, {"tmdb_id", "tmdb_id"}, {"tvdb_id", "tvdb_id"}} {
			value := client.clean(scalar(row.Attributes[spec.upstream]))
			state := releases.FactUnknown
			var values []string
			if spec.upstream == "size" {
				number, err := strconv.ParseInt(value, 10, 64)
				if err != nil || number <= 0 {
					value = ""
				}
			}
			if len(value) > 128 {
				value = ""
			}
			if value != "" {
				state = releases.FactObserved
				values = []string{value}
			}
			observation.Facts = append(observation.Facts, releases.Fact{Name: spec.fact, Values: values, State: state, ObservedAt: now})
		}
		result = append(result, observation)
	}
	return result, len(rows), nil
}

var _ error = Error{}
