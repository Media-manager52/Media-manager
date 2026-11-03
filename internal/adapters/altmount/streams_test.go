package altmount

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamClientPreparesWithAPIKeyAndReturnsOnlyThePath(t *testing.T) {
	var seen *http.Request
	var fields map[string]string
	var upload string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		fields = map[string]string{}
		for key, values := range r.MultipartForm.Value {
			fields[key] = values[0]
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(file)
		upload = header.Filename + ":" + string(body)
		_, _ = io.WriteString(w, `{"streams":[{"url":"http://altmount:8080/api/files/stream?path=%2Fnuvio%2FShow.S01E02%2FShow.S01E02.mkv&download_key=secret-download-key","name":"Show.S01E02.mkv"}],"_queue_item_id":42,"_cached":true}`)
	}))
	defer server.Close()
	client, err := NewStreamClient(server.URL, "the-api-key", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := client.Prepare(context.Background(), StreamRequest{Filename: "Show.S01.mm-abc.nzb", NZB: []byte("<nzb/>"), Category: "nuvio", Timeout: 100 * time.Second, Season: 1, Episode: 2})
	if err != nil {
		t.Fatal(err)
	}
	if seen.URL.Path != "/api/nzb/streams" || seen.Method != http.MethodPost || seen.Header.Get("X-Api-Key") != "the-api-key" {
		t.Fatalf("request %s %s key=%q", seen.Method, seen.URL.Path, seen.Header.Get("X-Api-Key"))
	}
	if fields["category"] != "nuvio" || fields["timeout"] != "100" || fields["season"] != "1" || fields["episode"] != "2" || upload != "Show.S01.mm-abc.nzb:<nzb/>" {
		t.Fatalf("fields=%v upload=%q", fields, upload)
	}
	if prepared != (PreparedStream{Path: "/nuvio/Show.S01E02/Show.S01E02.mkv", Name: "Show.S01E02.mkv", QueueItemID: 42, Cached: true}) {
		t.Fatalf("prepared=%+v", prepared)
	}
}

func TestStreamClientClassifiesFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"dead release", 500, `{"success":false,"error":{"message":"Processing failed","details":"fast-fail segment check failed: no such article"}}`, ErrStreamArticlesMissing},
		{"several videos", 200, `{"streams":[{"url":"http://a/api/files/stream?path=/a.mkv&download_key=k"},{"url":"http://a/api/files/stream?path=/b.mkv&download_key=k"}]}`, ErrStreamAmbiguous},
		{"no video", 200, `{"streams":[]}`, ErrStreamAmbiguous},
		{"path escapes the mount", 200, `{"streams":[{"url":"http://a/api/files/stream?path=/nuvio/../../etc/passwd&download_key=k"}]}`, ErrStreamResponse},
		{"unexpected address", 200, `{"streams":[{"url":"http://a/elsewhere?path=/a.mkv"}]}`, ErrStreamResponse},
		{"not json", 200, `<html>`, ErrStreamResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := NewStreamClient(server.URL, "key", true, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Prepare(context.Background(), StreamRequest{Filename: "a.nzb", NZB: []byte("x"), Category: "nuvio"})
			if !errors.Is(err, test.want) {
				t.Fatalf("err=%v, want %v", err, test.want)
			}
		})
	}
	t.Run("other AltMount error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"success":false,"error":{"message":"importer busy"}}`)
		}))
		defer server.Close()
		client, _ := NewStreamClient(server.URL, "key", true, nil)
		_, err := client.Prepare(context.Background(), StreamRequest{Filename: "a.nzb", NZB: []byte("x")})
		var apiErr APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 503 || errors.Is(err, ErrStreamArticlesMissing) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestStreamClientRefusesBadConfigurationAndUploads(t *testing.T) {
	for _, config := range [][3]string{{"http://altmount:8080", "", "http"}, {"http://altmount:8080", "key", "https-only"}, {"http://altmount:8080/api", "key", "http"}, {"http://user:pw@altmount:8080", "key", "http"}} {
		if _, err := NewStreamClient(config[0], config[1], config[2] == "http", nil); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%v accepted", config)
		}
	}
	client, err := NewStreamClient("http://altmount:8080", "key", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []StreamRequest{{Filename: "a.txt", NZB: []byte("x")}, {Filename: "../a.nzb", NZB: []byte("x")}, {Filename: "a.nzb"}, {Filename: "a.nzb", NZB: []byte("x"), Category: "bad\ncategory"}} {
		if _, err := client.Prepare(context.Background(), request); !errors.Is(err, ErrInvalidUpload) {
			t.Fatalf("%+v: err=%v", request, err)
		}
	}
	if strings.Contains(client.base.String(), "key") {
		t.Fatal("key leaked into the base URL")
	}
}
