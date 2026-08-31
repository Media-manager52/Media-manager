package newznab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
)

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
