package oldboys

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/media-manager/media-manager/internal/indexers"
	"github.com/media-manager/media-manager/internal/oldboyscredentials"
	"github.com/media-manager/media-manager/internal/releases"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

var testCredentials = oldboyscredentials.Credentials{APIToken: "api-secret-fixture", PassKey: "pid-secret-fixture", RSSKey: "rid-secret-fixture"}

func testClient(t *testing.T, transport http.RoundTripper, guard func(context.Context) error) (*Client, *indexers.Scheduler) {
	t.Helper()
	scheduler := indexers.NewScheduler(1, nil)
	id := uuid.NewV7()
	if err := scheduler.Configure(id.String(), indexers.Budget{MinimumInterval: time.Hour, FailureBackoff: time.Minute, MaximumBackoff: time.Hour, QueryLimit: 10, GrabLimit: 10, LimitWindow: time.Hour}); err != nil {
		t.Fatal(err)
	}
	client, err := New(id, testCredentials, scheduler, transport, guard)
	if err != nil {
		t.Fatal(err)
	}
	return client, scheduler
}
func TestNativeSearchKeepsUnknownEvidenceAndNumericIdentity(t *testing.T) {
	calls := 0
	client, scheduler := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "oldboys.pw" || r.URL.Path != "/api/nzbs/filter" || r.URL.Query().Get("categories[]") != "2" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("name") != "Operation X S2026 DANiSH" {
			t.Fatal("unexpected native request")
		}
		if r.Header.Get("Authorization") != "Bearer "+testCredentials.APIToken || r.Header.Get("User-Agent") != "MediaManager/1" {
			t.Fatal("request authentication/client identity incorrect")
		}
		return response(200, `{"data":[{"id":"198540","attributes":{"name":"Operation.X.S2026E01.DANiSH.1080p.WEB-DL","category_id":2,"size":null,"imdb_id":null,"tvdb_id":null,"details_link":"https://evil.invalid/?key=api-secret-fixture","media_info":null}},{"id":"12","attributes":{"name":"Foreign.Show.S01E01.DANiSH","category_id":1}},{"id":"13","attributes":{"name":"Other.Show.S01E01.NORDiC","category_id":2}}]}`), nil
	}), nil)
	request := SearchRequest{Name: "Operation X S2026 DANiSH", Category: 2, Page: 1}
	rows, total, err := client.ProbeSearch(context.Background(), request, indexers.WorkloadInteractive, nil)
	if err != nil || total != 3 || len(rows) != 2 {
		t.Fatalf("rows=%d total=%d err=%v", len(rows), total, err)
	}
	row := rows[0]
	if rows[1].Title != "Other.Show.S01E01.NORDiC" {
		t.Fatalf("Nordic release was not retained: %+v", rows[1].Title)
	}
	if row.NativeGUID != "198540" || row.DownloadURL != "" || row.IndexerID != client.id {
		t.Fatal("native identity or empty locator incorrect")
	}
	for _, fact := range row.Facts {
		if fact.State != releases.FactUnknown || len(fact.Values) != 0 {
			t.Fatal("missing evidence was invented")
		}
	}
	encoded, _ := json.Marshal(row)
	if strings.Contains(string(encoded), testCredentials.APIToken) || strings.Contains(string(encoded), "evil.invalid") {
		t.Fatal("unsafe evidence retained")
	}
	if _, err := client.Search(context.Background(), request, indexers.WorkloadInteractive, nil); err == nil || calls != 1 {
		t.Fatal("pacing allowed another request")
	}
	if scheduler.Snapshot(client.id.String()).LastOutcome != indexers.OutcomeSuccess {
		t.Fatal("success not recorded")
	}
}
func TestPIDDownloadPreservesPasswordNZBAndRejectsURLs(t *testing.T) {
	body := `<?xml version="1.0"?><nzb><head><meta type="password">package-password</meta></head><file subject="&quot;video.rar&quot; yEnc"><groups><group>alt.binaries.test</group></groups><segments><segment bytes="12" number="1">message@test</segment></segments></file></nzb>`
	calls := 0
	client, _ := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/nzbs/download/198540."+testCredentials.PassKey || strings.Contains(r.URL.String(), testCredentials.RSSKey) || r.Header.Get("Authorization") != "Bearer "+testCredentials.APIToken {
			t.Fatal("PID route incorrect")
		}
		return response(200, body), nil
	}), nil)
	for _, id := range []string{"https://evil.invalid/123", "../123", "123.key", "0", "-2", "123?key=x"} {
		if _, err := client.GetNZBPackageGuarded(context.Background(), id, nil); err == nil {
			t.Fatal("unsafe ID accepted")
		}
	}
	pack, err := client.GetNZBPackageGuarded(context.Background(), "198540", nil)
	if err != nil || string(pack.Body) != body || calls != 1 {
		t.Fatal("NZB validation or password preservation failed", err)
	}
}
func TestRedirectsErrorsAndGuardsNeverExposeCredentials(t *testing.T) {
	for _, status := range []int{302, 401, 403, 404, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			client, scheduler := testClient(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				resp := response(status, testCredentials.APIToken)
				resp.Header.Set("Location", "https://evil.invalid/"+testCredentials.PassKey)
				return resp, nil
			}), nil)
			_, err := client.GetNZBPackageGuarded(context.Background(), "123", nil)
			if err == nil || calls != 1 || strings.Contains(err.Error(), testCredentials.PassKey) || strings.Contains(err.Error(), testCredentials.APIToken) {
				t.Fatal("redirect followed or error exposed secrets")
			}
			if status == 429 && scheduler.Snapshot(client.id.String()).LastOutcome != indexers.OutcomeRateLimited {
				t.Fatal("rate limit not recorded")
			}
		})
	}
	sentinel := errors.New("stale selection")
	client, _ := testClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("guarded request sent"); return nil, nil }), func(context.Context) error { return sentinel })
	if _, err := client.GetNZBPackageGuarded(context.Background(), "123", nil); !errors.Is(err, sentinel) {
		t.Fatal("guard did not stop send")
	}
	client, _ = testClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("URL contains " + testCredentials.PassKey)
	}), nil)
	_, err := client.GetNZBPackageGuarded(context.Background(), "123", nil)
	if err == nil || strings.Contains(err.Error(), testCredentials.PassKey) {
		t.Fatal("transport error leaked key")
	}
}
func TestBoundedParsingAndNegativeControls(t *testing.T) {
	for _, body := range []string{`<html>login</html>`, `{"data":null}`, `{"data":{}}`, strings.Repeat("x", searchLimit+1)} {
		t.Run("invalid-response", func(t *testing.T) {
			client, _ := testClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil }), nil)
			if _, err := client.Search(context.Background(), SearchRequest{Name: "negative", Category: 1, Page: 1}, indexers.WorkloadCommissioning, nil); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
	client, _ := testClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"data":[{"id":"1","attributes":{"name":"wrong category","category_id":2}}]}`), nil
	}), nil)
	rows, total, err := client.ProbeSearch(context.Background(), SearchRequest{Name: "negative", Category: 1, Page: 1}, indexers.WorkloadCommissioning, nil)
	if err != nil || len(rows) != 0 || total != 1 {
		t.Fatal("negative control lost raw result count")
	}
	value := testCredentials.PassKey
	for depth := 0; depth < 4; depth++ {
		if strings.Contains(client.clean(value), testCredentials.PassKey) || client.clean(value) == value {
			t.Fatal("encoded secret retained")
		}
		value = url.QueryEscape(value)
	}
}
