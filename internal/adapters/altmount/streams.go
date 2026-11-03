package altmount

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// StreamClient prepares one NZB for immediate playback through AltMount's
// POST /api/nzb/streams endpoint. Unlike the queue API, that endpoint accepts
// only AltMount's API key, so the key stays in Media Manager and the caller
// receives nothing but the prepared file's path on the read-only mount.
type StreamClient struct {
	base   *url.URL
	apiKey string
	http   *http.Client
}

// StreamRequest is one NZB to prepare.
type StreamRequest struct {
	Filename string
	NZB      []byte
	Category string
	// Timeout is how long AltMount may spend making the file readable.
	Timeout time.Duration
	// Season and Episode select one episode from a season pack; zero for a
	// movie.
	Season  int
	Episode int
}

// PreparedStream is the playable file AltMount prepared. Path is relative to
// AltMount's mount root and always starts with "/".
type PreparedStream struct {
	Path        string `json:"path"`
	Name        string `json:"name,omitempty"`
	QueueItemID int64  `json:"queue_item_id,omitempty"`
	Cached      bool   `json:"cached"`
}

var (
	// ErrStreamArticlesMissing means the release can never play: its Usenet
	// articles are gone or it holds no playable file.
	ErrStreamArticlesMissing = errors.New("AltMount reports the release's articles are missing")
	// ErrStreamAmbiguous means AltMount found zero or several videos; one is
	// never picked arbitrarily.
	ErrStreamAmbiguous = errors.New("AltMount did not identify exactly one playable video")
	ErrStreamResponse  = errors.New("AltMount returned an invalid stream response")
)

// deadReleaseMarkers are AltMount failure texts that mean the release itself is
// unusable, so retrying the same NZB can never succeed.
var deadReleaseMarkers = []string{
	"no such article",
	"total size is zero",
	"fast-fail segment check failed",
	"missing segments",
	"contains only par2",
	"contains no valid files",
}

// NewStreamClient validates the AltMount origin and API key.
func NewStreamClient(baseURL, apiKey string, allowHTTP bool, transport http.RoundTripper) (*StreamClient, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") || (base.Scheme != "https" && !(allowHTTP && base.Scheme == "http")) {
		return nil, ErrInvalidConfig
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" || hasControl(apiKey) {
		return nil, ErrInvalidConfig
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	base.Path = ""
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("AltMount redirects are disabled") }}
	return &StreamClient{base: base, apiKey: apiKey, http: client}, nil
}

// Prepare uploads the NZB and waits until AltMount can serve its one video.
func (client *StreamClient) Prepare(ctx context.Context, request StreamRequest) (PreparedStream, error) {
	if !strings.HasSuffix(strings.ToLower(request.Filename), ".nzb") || strings.ContainsAny(request.Filename, "/\\") || hasControl(request.Filename) || len(request.NZB) == 0 || int64(len(request.NZB)) > maximumUploadSize || hasControl(request.Category) || request.Season < 0 || request.Episode < 0 {
		return PreparedStream{}, ErrInvalidUpload
	}
	timeout := int(request.Timeout / time.Second)
	if timeout < 1 {
		timeout = 100
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", request.Filename)
	if err != nil {
		return PreparedStream{}, err
	}
	if _, err := file.Write(request.NZB); err != nil {
		return PreparedStream{}, err
	}
	fields := [][2]string{{"timeout", strconv.Itoa(timeout)}, {"category", request.Category}, {"season", strconv.Itoa(request.Season)}, {"episode", strconv.Itoa(request.Episode)}}
	for _, field := range fields {
		if err := form.WriteField(field[0], field[1]); err != nil {
			return PreparedStream{}, err
		}
	}
	if err := form.Close(); err != nil {
		return PreparedStream{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.base.JoinPath("/api/nzb/streams").String(), &body)
	if err != nil {
		return PreparedStream{}, err
	}
	httpRequest.Header.Set("X-Api-Key", client.apiKey)
	httpRequest.Header.Set("Content-Type", form.FormDataContentType())
	response, err := client.http.Do(httpRequest)
	if err != nil {
		return PreparedStream{}, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil {
		return PreparedStream{}, err
	}
	if len(payload) > 1<<20 {
		return PreparedStream{}, ErrResponseLimit
	}
	if response.StatusCode != http.StatusOK {
		if deadRelease(string(payload)) {
			return PreparedStream{}, fmt.Errorf("%w (HTTP %d)", ErrStreamArticlesMissing, response.StatusCode)
		}
		return PreparedStream{}, APIError{StatusCode: response.StatusCode, Message: streamErrorMessage(payload)}
	}
	var result struct {
		Streams []struct {
			URL  string `json:"url"`
			Name string `json:"name"`
		} `json:"streams"`
		QueueItemID int64 `json:"_queue_item_id"`
		Cached      bool  `json:"_cached"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return PreparedStream{}, ErrStreamResponse
	}
	if len(result.Streams) != 1 {
		return PreparedStream{}, ErrStreamAmbiguous
	}
	prepared, ok := streamPath(result.Streams[0].URL)
	if !ok {
		return PreparedStream{}, ErrStreamResponse
	}
	return PreparedStream{Path: prepared, Name: result.Streams[0].Name, QueueItemID: result.QueueItemID, Cached: result.Cached}, nil
}

// streamPath extracts the mount-relative file path from AltMount's stream URL
// (/api/files/stream?path=...&download_key=...). The download key is dropped.
func streamPath(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Path != "/api/files/stream" {
		return "", false
	}
	value := parsed.Query().Get("path")
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	if value == "/" || hasControl(value) || path.Clean(value) != value {
		return "", false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", false
		}
	}
	return value, true
}

func deadRelease(detail string) bool {
	detail = strings.ToLower(detail)
	for _, marker := range deadReleaseMarkers {
		if strings.Contains(detail, marker) {
			return true
		}
	}
	return false
}

// streamErrorMessage keeps AltMount's own short error text for logs and
// operators, never the request.
func streamErrorMessage(payload []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Details string `json:"details"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(payload, &envelope) == nil {
		message := strings.TrimSpace(strings.Join([]string{envelope.Error.Message, envelope.Error.Details, envelope.Message}, " "))
		if len(message) > 300 {
			message = message[:300]
		}
		return message
	}
	return ""
}
