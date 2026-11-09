package newznab

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/media-manager/media-manager/internal/releases"
)

func TestTreasureMapsNFOIsSkippedWithoutRequestsOrBudgetButNZBStillWorks(t *testing.T) {
	t.Parallel()
	info := releases.RawField{Name: "info", Value: "https://treasure-maps.com/api?t=getnfo&id=" + treasureMapsReleaseID + "&raw=1"}
	flag := releases.RawField{Name: "nfo", Value: "1"}
	for _, fields := range [][]releases.RawField{{info}, {flag}, {info, flag}} {
		transport := &recordingTransport{respond: func(request *http.Request) *http.Response {
			if request.URL.Host != "file.treasure-maps.com" {
				t.Fatal("optional NFO must not reach the transport")
			}
			return textResponse(request, http.StatusOK, redirectTestNZB, http.Header{"Content-Type": []string{"application/x-nzb"}})
		}}
		client := treasureMapsTestClient(t, transport)
		observation := releases.Observation{NativeGUID: treasureMapsReleaseID, Fields: fields}
		before := client.scheduler.Snapshot(client.config.IndexerID)
		if client.AdvertisesNFO(observation) {
			t.Fatal("known unsupported NFO must not be advertised to acquisition")
		}
		if _, err := client.InspectAdvertisedNFO(context.Background(), observation); !errors.Is(err, ErrNFOUnavailable) {
			t.Fatalf("direct inspection must also skip: %v", err)
		}
		if len(transport.requests) != 0 || !reflect.DeepEqual(before, client.scheduler.Snapshot(client.config.IndexerID)) {
			t.Fatal("skipped NFO spent a request, pacing or budget")
		}
		pack, err := client.GetNZBPackageFromLocatorGuarded(context.Background(), treasureMapsReleaseID, "native:"+treasureMapsReleaseID, nil)
		if err != nil || len(pack.Body) == 0 || len(transport.requests) != 1 {
			t.Fatalf("normal bounded NZB retrieval must remain available: requests=%d error=%v", len(transport.requests), err)
		}
	}
}

func TestNFOCompatibilityExceptionOnlyForVerifiedEndpoint(t *testing.T) {
	t.Parallel()
	for _, check := range []struct {
		base string
		want bool
	}{
		{"https://treasure-maps.com/api", false},
		{"https://TREASURE-MAPS.COM/api/", false},
		{"https://treasure-maps.com:443/api", false},
		{"https://abnzb.com/api", true},
		{"https://drunkenslug.com/api", true},
		{"https://www.treasure-maps.com/api", true},
		{"https://file.treasure-maps.com/api", true},
		{"https://treasure-maps.com.evil.example/api", true},
		{"https://treasure-maps.com:8443/api", true},
		{"http://treasure-maps.com/api", true},
		{"https://treasure-maps.com/api/v2", true},
	} {
		observation := releases.Observation{NativeGUID: treasureMapsReleaseID, Fields: []releases.RawField{{Name: "info", Value: check.base + "?t=getnfo&id=" + treasureMapsReleaseID}}}
		_, _, ok := AdvertisedNFOFunction(observation, check.base)
		if ok != check.want {
			t.Errorf("%s: advertised=%v, want=%v", check.base, ok, check.want)
		}
	}
}
