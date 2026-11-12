package altmount

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func fixtureUpload() UploadRequest {
	body := `<nzb><file subject="Example release"/></nzb>`
	return UploadRequest{Filename: "example-attempt-17.nzb", Category: "media-manager", RelativePath: "attempt-17", Priority: PriorityNormal, Size: int64(len(body)), Open: func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(body)), nil
	}}
}

// An uncertain upload is reported for reconciliation and is never automatically sent twice.
func TestUploadKeepsUncertainResponsesAmbiguousWithoutResubmitting(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "malformed-json", body: `{"data":`},
		{name: "missing-id", body: `{"success":true,"data":{"status":"pending"}}`},
		{name: "negative-id", body: `{"success":true,"data":{"id":-1}}`},
		{name: "rejected-envelope", body: `{"success":false,"error":"private provider detail"}`},
		{name: "oversized-response", body: strings.Repeat("x", 129)},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/queue/upload" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL, AllowHTTP: true, AllowAnonymousAdmin: true, MaxResponseSize: 128}, nil)
			if err != nil {
				t.Fatal(err)
			}
			item, err := client.UploadNZB(t.Context(), fixtureUpload())
			var ambiguous AmbiguousSubmissionError
			if !errors.As(err, &ambiguous) || errors.Unwrap(err) == nil || item.ID != 0 || calls != 1 {
				t.Fatalf("upload = %+v, %v; requests=%d, want one ambiguous attempt", item, err, calls)
			}
			if strings.Contains(err.Error(), "private provider detail") {
				t.Fatal("upload error exposed provider response text")
			}
			if test.name == "oversized-response" && !errors.Is(err, ErrResponseLimit) {
				t.Fatalf("oversized response lost its cause: %v", err)
			}
		})
	}
}

// A rejected session permits one login and replay of the exact NZB, with no retry loop.
func TestUploadReauthenticatesOnceAndPreservesTheExactPayload(t *testing.T) {
	for _, succeeds := range []bool{true, false} {
		t.Run(map[bool]string{true: "accepted-after-login", false: "still-unauthorized"}[succeeds], func(t *testing.T) {
			var requests []string
			uploads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				if r.URL.Path == "/api/auth/login" {
					http.SetCookie(w, &http.Cookie{Name: "JWT", Value: "example-session", Path: "/"})
					_, _ = io.WriteString(w, `{"data":{"user":{"id":"example-owner"}}}`)
					return
				}
				if r.URL.Path != "/api/queue/upload" || r.Method != http.MethodPost {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				uploads++
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				file, header, err := r.FormFile("file")
				if err != nil {
					t.Error(err)
					return
				}
				defer file.Close()
				body, err := io.ReadAll(file)
				if err != nil || header.Filename != "example-attempt-17.nzb" || string(body) != `<nzb><file subject="Example release"/></nzb>` || r.FormValue("category") != "media-manager" || r.FormValue("relative_path") != "attempt-17" || r.FormValue("priority") != "2" {
					t.Errorf("changed upload: filename=%q body=%q fields=%v err=%v", header.Filename, body, r.MultipartForm.Value, err)
				}
				if uploads == 1 || !succeeds {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				cookie, err := r.Cookie("JWT")
				if err != nil || cookie.Value != "example-session" {
					t.Errorf("replay omitted the new session cookie: %v", err)
				}
				_, _ = io.WriteString(w, `{"success":true,"data":{"id":17,"status":"pending"}}`)
			}))
			defer server.Close()
			client, err := New(Config{BaseURL: server.URL, AllowHTTP: true, Username: "example-owner", Password: "example-password"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			item, err := client.UploadNZB(t.Context(), fixtureUpload())
			if succeeds {
				if err != nil || item.ID != 17 {
					t.Fatalf("authenticated upload = %+v, %v", item, err)
				}
			} else {
				var apiErr APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized || item.ID != 0 {
					t.Fatalf("repeated authentication failure = %+v, %v", item, err)
				}
			}
			if !reflect.DeepEqual(requests, []string{"/api/queue/upload", "/api/auth/login", "/api/queue/upload"}) {
				t.Fatalf("request sequence = %v", requests)
			}
		})
	}
}

type uploadTransport func(*http.Request) (*http.Response, error)

func (transport uploadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// Invalid local NZBs are rejected before network I/O; a lost response stays ambiguous.
func TestUploadDistinguishesLocalFailureFromUnknownTransmission(t *testing.T) {
	calls := 0
	lost := errors.New("example connection lost after upload")
	client, err := New(Config{BaseURL: "https://altmount.invalid", AllowAnonymousAdmin: true}, uploadTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, lost
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*UploadRequest)
	}{
		{name: "truncated-body", change: func(upload *UploadRequest) { upload.Size++ }},
		{name: "escaping-name", change: func(upload *UploadRequest) { upload.Filename = "../example.nzb" }},
		{name: "escaping-destination", change: func(upload *UploadRequest) { upload.RelativePath = "../elsewhere" }},
		{name: "unsupported-file", change: func(upload *UploadRequest) { upload.Filename = "example.zip" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			upload := fixtureUpload()
			test.change(&upload)
			if _, err := client.UploadNZB(context.Background(), upload); !errors.Is(err, ErrInvalidUpload) {
				t.Fatalf("invalid upload = %v", err)
			}
			if calls != 0 {
				t.Fatalf("invalid upload contacted AltMount %d times", calls)
			}
		})
	}
	_, err = client.UploadNZB(t.Context(), fixtureUpload())
	var ambiguous AmbiguousSubmissionError
	if !errors.As(err, &ambiguous) || !errors.Is(err, lost) || calls != 1 {
		t.Fatalf("lost response = %v, requests=%d", err, calls)
	}
}
