package seerr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestSetMediaStatusUsesSupportedBoundedEndpoint(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/media/77/available" || request.URL.RawQuery != "" {
			t.Fatalf("unexpected status request: %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("X-Api-Key") != "secret" || request.Header.Get("Content-Type") != "application/json" {
			t.Fatal("status request omitted required authentication or content type")
		}
		var payload struct {
			Seasons []struct {
				SeasonNumber int16 `json:"seasonNumber"`
			} `json:"seasons"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Seasons) != 2 || payload.Seasons[0].SeasonNumber != 2 || payload.Seasons[1].SeasonNumber != 4 {
			t.Fatalf("unexpected status seasons: %+v", payload.Seasons)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":77,"mediaType":"tv","status":5}`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, APIKey: "secret", AllowHTTP: true, ResponseLimit: 1024}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	media, err := client.SetMediaStatus(context.Background(), 77, MediaStatusAvailable, []int16{2, 4, 2})
	if err != nil || media.ID != 77 || calls != 1 {
		t.Fatalf("status result=%+v calls=%d err=%v", media, calls, err)
	}
}

func TestSetMediaStatusRejectsRedirectAndMismatchedIdentity(t *testing.T) {
	redirectTargetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirectTargetCalls++ }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/media/77/available" {
			http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
			return
		}
		_, _ = writer.Write([]byte(`{"id":78}`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, APIKey: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetMediaStatus(context.Background(), 77, MediaStatusAvailable, nil); err == nil || redirectTargetCalls != 0 {
		t.Fatalf("redirect followed or accepted: calls=%d err=%v", redirectTargetCalls, err)
	}

	mismatch := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"id":78}`))
	}))
	defer mismatch.Close()
	client, err = New(Config{BaseURL: mismatch.URL, APIKey: "secret", AllowHTTP: true}, mismatch.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetMediaStatus(context.Background(), 77, MediaStatusAvailable, nil); err == nil {
		t.Fatal("mismatched response media identity was accepted")
	}
}

func TestParseWebhookAcceptsLifecycleWakeups(t *testing.T) {
	for _, notification := range []string{"MEDIA_APPROVED", "MEDIA_DECLINED", "MEDIA_AVAILABLE"} {
		body := []byte(`{"notification_type":"` + notification + `","request_id":"42","media_type":"tv"}`)
		if _, err := ParseWebhook(body); err != nil {
			t.Fatalf("%s webhook rejected: %v", notification, err)
		}
	}
	if _, err := ParseWebhook([]byte(`{"notification_type":"MEDIA_APPROVED","request_id":"invalid","media_type":"tv"}`)); !errors.Is(err, ErrInvalidWebhook) {
		t.Fatalf("invalid request ID accepted: %v", err)
	}
}

func TestParseWebhookAcceptsEmptyOptionalProviderIdentifier(t *testing.T) {
	event, err := ParseWebhook([]byte(`{"notification_type":"MEDIA_AUTO_APPROVED","request_id":"1","media_type":"movie","media_tmdbid":"860508","media_tvdbid":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.RequestID.String() != "1" || event.MediaTMDBID.String() != "860508" || event.MediaTVDBID.String() != "" {
		t.Fatalf("unexpected webhook identifiers: %+v", event)
	}
}

func TestRequestReturnsTypedNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, APIKey: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Request(context.Background(), 42); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("not-found response was not typed: %v", err)
	}
}

func TestSearchUsesBoundedEncodedQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/search" || request.URL.Query().Get("query") != "Dune & Sand" || request.URL.Query().Get("page") != "2" {
			t.Fatalf("unexpected search URL: %s", request.URL.String())
		}
		if request.URL.RawQuery != "page=2&query=Dune%20%26%20Sand" {
			t.Fatalf("unexpected raw search query: %s", request.URL.RawQuery)
		}
		if request.Header.Get("X-Api-Key") != "secret" {
			t.Fatal("missing Seerr API key")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"page":2,"totalPages":3,"totalResults":1,"results":[{"id":42,"mediaType":"movie","title":"Dune"}]}`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, APIKey: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Search(context.Background(), "Dune & Sand", 2)
	if err != nil || len(result.Results) != 1 || result.Results[0].ID != 42 {
		t.Fatalf("search result=%+v err=%v", result, err)
	}
	if _, err := client.Search(context.Background(), "x", 1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("short query accepted: %v", err)
	}
}

func TestMetadataParsesRichTMDBFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/tv/42" {
			t.Fatalf("unexpected metadata path: %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":42,"name":"Series","originalName":"Original","overview":"Overview","originalLanguage":"da","firstAirDate":"2026-09-01","status":"Returning Series","episodeRunTime":[45],"posterPath":"/poster.jpg","backdropPath":"/backdrop.jpg","voteAverage":8.2,"voteCount":100,"genres":[{"id":18,"name":"Drama"}],"originCountry":["DK"],"networks":[{"id":1,"name":"DR","originCountry":"DK"}],"externalIds":{"tvdbId":77,"imdbId":"tt1234567"}}`))
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, APIKey: "secret", AllowHTTP: true}, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := client.Metadata(context.Background(), "tv", 42)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.OriginalName != "Original" || metadata.Overview != "Overview" || metadata.OriginalLanguage != "da" || len(metadata.EpisodeRunTime) != 1 || metadata.EpisodeRunTime[0] != 45 || len(metadata.Genres) != 1 || metadata.Networks[0].Name != "DR" || metadata.VoteCount != 100 {
		t.Fatalf("rich metadata was not decoded: %+v", metadata)
	}
}

func TestDecideRequestPostsExactDecisionAndVerifiesResult(t *testing.T) {
	for _, test := range []struct {
		decision RequestDecision
		status   int
	}{{DecisionApprove, RequestStatusApproved}, {DecisionDecline, RequestStatusDeclined}} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodPost || request.URL.Path != "/api/v1/request/42/"+string(test.decision) {
				t.Fatalf("unexpected decision call %s %s", request.Method, request.URL.Path)
			}
			if request.Header.Get("X-Api-Key") != "secret" {
				t.Fatal("missing Seerr API key")
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"id":42,"status":` + strconv.Itoa(test.status) + `,"type":"movie","media":{"id":7}}`))
		}))
		client, err := New(Config{BaseURL: server.URL, APIKey: "secret", AllowHTTP: true}, server.Client().Transport)
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.DecideRequest(context.Background(), 42, test.decision)
		server.Close()
		if err != nil || result.ID != 42 || result.Status != test.status {
			t.Fatalf("%s result=%+v err=%v", test.decision, result, err)
		}
	}
}

func TestDecideRequestRejectsMismatchedOrUnconfirmedResults(t *testing.T) {
	for name, body := range map[string]string{
		"other request":  `{"id":43,"status":2}`,
		"still pending":  `{"id":42,"status":1}`,
		"declined twist": `{"id":42,"status":3}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write([]byte(body))
		}))
		client, err := New(Config{BaseURL: server.URL, APIKey: "secret", AllowHTTP: true}, server.Client().Transport)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.DecideRequest(context.Background(), 42, DecisionApprove)
		server.Close()
		if err == nil {
			t.Fatalf("%s: approval was accepted without Seerr confirming it", name)
		}
	}
	client, err := New(Config{BaseURL: "https://seerr.invalid", APIKey: "secret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DecideRequest(context.Background(), 42, RequestDecision("delete")); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("unknown decision err=%v, want ErrInvalidConfig", err)
	}
	if _, err := client.DecideRequest(context.Background(), 0, DecisionApprove); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid id err=%v, want ErrInvalidConfig", err)
	}
}
