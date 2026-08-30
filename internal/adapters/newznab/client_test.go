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
