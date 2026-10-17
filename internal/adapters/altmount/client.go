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
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/media-manager/media-manager/internal/library"
)

const maximumUploadSize = int64(100 << 20)

type Config struct {
	BaseURL             string
	Username            string
	Password            string
	AllowAnonymousAdmin bool
	AllowHTTP           bool
	RequestTimeout      time.Duration
	MaxResponseSize     int64
}

type HTTPClient struct {
	config  Config
	base    *url.URL
	http    *http.Client
	loginMu sync.Mutex
}

type APIError struct {
	StatusCode int
	Message    string
}

func (err APIError) Error() string { return fmt.Sprintf("AltMount returned HTTP %d", err.StatusCode) }

type AmbiguousSubmissionError struct{ Cause error }

func (err AmbiguousSubmissionError) Error() string {
	return "AltMount upload result is unknown; reconcile by the unique upload filename"
}
func (err AmbiguousSubmissionError) Unwrap() error { return err.Cause }

var (
	ErrInvalidConfig = errors.New("invalid AltMount configuration")
	ErrInvalidUpload = errors.New("invalid AltMount upload")
	ErrResponseLimit = errors.New("AltMount response exceeds the configured limit")
	ErrSessionCookie = errors.New("AltMount did not issue a JWT session cookie")
	ErrLoginRequired = errors.New("AltMount requires an authenticated login")
	ErrAdminRequired = errors.New("AltMount did not expose an anonymous admin session")
)

func New(config Config, transport http.RoundTripper) (*HTTPClient, error) {
	base, err := url.Parse(strings.TrimSpace(config.BaseURL))
	hasUsername, hasPassword := strings.TrimSpace(config.Username) != "", config.Password != ""
	hasSessionCredentials := hasUsername && hasPassword
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") || (base.Scheme != "https" && !(config.AllowHTTP && base.Scheme == "http")) || hasUsername != hasPassword || hasSessionCredentials == config.AllowAnonymousAdmin {
		return nil, ErrInvalidConfig
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 30 * time.Second
	}
	if config.MaxResponseSize <= 0 {
		config.MaxResponseSize = 8 << 20
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Transport: transport, Jar: jar, Timeout: config.RequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("AltMount redirects are disabled") }}
	base.Path = ""
	return &HTTPClient{config: config, base: base, http: client}, nil
}

func (client *HTTPClient) Login(ctx context.Context) (Session, error) {
	client.loginMu.Lock()
	defer client.loginMu.Unlock()
	if client.config.AllowAnonymousAdmin {
		var authConfig struct {
			LoginRequired bool `json:"login_required"`
		}
		if err := client.jsonOnce(ctx, http.MethodGet, "/api/auth/config", nil, &authConfig); err != nil {
			return Session{}, err
		}
		if authConfig.LoginRequired {
			return Session{}, ErrLoginRequired
		}
		var user User
		if err := client.jsonOnce(ctx, http.MethodGet, "/api/user", nil, &user); err != nil {
			return Session{}, err
		}
		if strings.TrimSpace(user.ID) == "" || !user.IsAdmin {
			return Session{}, ErrAdminRequired
		}
		return Session{User: user, AuthenticatedAt: time.Now().UTC()}, nil
	}
	var response struct {
		User User `json:"user"`
	}
	if err := client.jsonOnce(ctx, http.MethodPost, "/api/auth/login", map[string]string{"username": client.config.Username, "password": client.config.Password}, &response); err != nil {
		return Session{}, err
	}
	found := false
	for _, cookie := range client.http.Jar.Cookies(client.base) {
		if cookie.Name == "JWT" && cookie.Value != "" {
			found = true
			break
		}
	}
	if !found {
		return Session{}, ErrSessionCookie
	}
	return Session{User: response.User, AuthenticatedAt: time.Now().UTC()}, nil
}

func (client *HTTPClient) Refresh(ctx context.Context) error {
	if client.config.AllowAnonymousAdmin {
		_, err := client.Login(ctx)
		return err
	}
	return client.json(ctx, http.MethodPost, "/api/user/refresh", nil, nil)
}
func (client *HTTPClient) Logout(ctx context.Context) error {
	if client.config.AllowAnonymousAdmin {
		return nil
	}
	return client.json(ctx, http.MethodPost, "/api/auth/logout", nil, nil)
}

func (client *HTTPClient) Queue(ctx context.Context, id QueueID) (QueueItem, error) {
	var item QueueItem
	err := client.json(ctx, http.MethodGet, "/api/queue/"+strconv.FormatInt(int64(id), 10), nil, &item)
	return item, err
}

func (client *HTTPClient) ListQueue(ctx context.Context, query QueueQuery) (QueuePage, error) {
	values := make(url.Values)
	if query.Status != nil {
		values.Set("status", string(*query.Status))
	}
	if query.Search != "" {
		values.Set("search", query.Search)
	}
	if query.Since != nil {
		values.Set("since", query.Since.UTC().Format(time.RFC3339Nano))
	}
	if query.SortBy != "" {
		values.Set("sort_by", query.SortBy)
	}
	if query.SortOrder != "" {
		values.Set("sort_order", query.SortOrder)
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	if query.Offset > 0 {
		values.Set("offset", strconv.Itoa(query.Offset))
	}
	var page QueuePage
	if err := client.json(ctx, http.MethodGet, "/api/queue?"+values.Encode(), nil, &page.Items); err != nil {
		return QueuePage{}, err
	}
	return page, nil
}

// HealthItem is one file AltMount's health checker tracks.
type HealthItem struct {
	ID        int64  `json:"id"`
	FilePath  string `json:"file_path"`
	Status    string `json:"status"`
	LastError string `json:"last_error"`
}

// ListCorruptedFiles returns the files AltMount's health checker marked
// corrupted (Usenet articles missing; playback fails). It pages through the
// whole list.
func (client *HTTPClient) ListCorruptedFiles(ctx context.Context) ([]HealthItem, error) {
	const pageSize = 200
	all := []HealthItem{}
	for offset := 0; offset < 100_000; offset += pageSize {
		values := url.Values{"status": {"corrupted"}, "limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(offset)}}
		var page []HealthItem
		if err := client.json(ctx, http.MethodGet, "/api/health?"+values.Encode(), nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < pageSize {
			break
		}
	}
	return all, nil
}

func (client *HTTPClient) UploadNZB(ctx context.Context, upload UploadRequest) (QueueItem, error) {
	if err := validateUpload(upload); err != nil {
		return QueueItem{}, err
	}
	body, contentType, err := encodeUpload(upload)
	if err != nil {
		return QueueItem{}, err
	}
	var item QueueItem
	status, response, requestErr := client.raw(ctx, http.MethodPost, "/api/queue/upload", contentType, body)
	if requestErr != nil {
		return QueueItem{}, AmbiguousSubmissionError{Cause: requestErr}
	}
	if status == http.StatusUnauthorized {
		if _, err := client.Login(ctx); err != nil {
			return QueueItem{}, err
		}
		body, contentType, err = encodeUpload(upload)
		if err != nil {
			return QueueItem{}, err
		}
		status, response, requestErr = client.raw(ctx, http.MethodPost, "/api/queue/upload", contentType, body)
		if requestErr != nil {
			return QueueItem{}, AmbiguousSubmissionError{Cause: requestErr}
		}
	}
	if status < 200 || status >= 300 {
		return QueueItem{}, APIError{StatusCode: status}
	}
	if err := decodeEnvelope(response, &item); err != nil {
		return QueueItem{}, AmbiguousSubmissionError{Cause: err}
	}
	if item.ID <= 0 {
		return QueueItem{}, AmbiguousSubmissionError{Cause: errors.New("AltMount returned no numeric queue ID")}
	}
	return item, nil
}

func (client *HTTPClient) RequestCancel(ctx context.Context, id QueueID) error {
	return client.json(ctx, http.MethodPost, "/api/queue/"+strconv.FormatInt(int64(id), 10)+"/cancel", nil, nil)
}
func (client *HTTPClient) DeleteQueueRecord(ctx context.Context, id QueueID) error {
	return client.json(ctx, http.MethodDelete, "/api/queue/"+strconv.FormatInt(int64(id), 10), nil, nil)
}

func (client *HTTPClient) CancelAndDelete(ctx context.Context, id QueueID, policy PollPolicy) (RemoveResult, error) {
	if policy.Interval <= 0 {
		policy.Interval = 2 * time.Second
	}
	if policy.Timeout <= 0 {
		policy.Timeout = 2 * time.Minute
	}
	item, err := client.Queue(ctx, id)
	if err != nil {
		return RemoveResult{}, err
	}
	result := RemoveResult{Final: &item}
	if item.Status == StatusProcessing {
		if err := client.RequestCancel(ctx, id); err != nil {
			return result, err
		}
		result.Cancelled = true
		deadline := time.Now().Add(policy.Timeout)
		for item.Status == StatusProcessing {
			if time.Now().After(deadline) {
				return result, context.DeadlineExceeded
			}
			timer := time.NewTimer(policy.Interval)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return result, ctx.Err()
			}
			item, err = client.Queue(ctx, id)
			if err != nil {
				return result, err
			}
			result.Final = &item
		}
	}
	if err := client.DeleteQueueRecord(ctx, id); err != nil {
		return result, err
	}
	result.Deleted = true
	return result, nil
}

func (client *HTTPClient) json(ctx context.Context, method, path string, input, output any) error {
	err := client.jsonOnce(ctx, method, path, input, output)
	var apiError APIError
	if errors.As(err, &apiError) && apiError.StatusCode == http.StatusUnauthorized {
		if _, loginErr := client.Login(ctx); loginErr != nil {
			return loginErr
		}
		return client.jsonOnce(ctx, method, path, input, output)
	}
	return err
}

func (client *HTTPClient) jsonOnce(ctx context.Context, method, path string, input, output any) error {
	var body []byte
	var err error
	contentType := ""
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
		contentType = "application/json"
	}
	status, response, err := client.raw(ctx, method, path, contentType, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return APIError{StatusCode: status}
	}
	if output == nil || len(bytes.TrimSpace(response)) == 0 {
		return nil
	}
	return decodeEnvelope(response, output)
}

func (client *HTTPClient) raw(ctx context.Context, method, path, contentType string, body []byte) (int, []byte, error) {
	if !strings.HasPrefix(path, "/api/") || strings.Contains(path, "\\") || strings.Contains(path, "//") {
		return 0, nil, ErrInvalidConfig
	}
	endpoint := *client.base
	parts := strings.SplitN(path, "?", 2)
	endpoint.Path = parts[0]
	if len(parts) == 2 {
		endpoint.RawQuery = parts[1]
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "MediaManager/1")
	response, err := client.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, client.config.MaxResponseSize+1))
	if err != nil {
		return response.StatusCode, nil, err
	}
	if int64(len(data)) > client.config.MaxResponseSize {
		return response.StatusCode, nil, ErrResponseLimit
	}
	return response.StatusCode, data, nil
}

func decodeEnvelope(body []byte, output any) error {
	var envelope struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	if envelope.Success != nil && !*envelope.Success {
		return errors.New("AltMount rejected the request")
	}
	payload := envelope.Data
	if len(payload) == 0 || bytes.Equal(payload, []byte("null")) {
		payload = body
	}
	return json.Unmarshal(payload, output)
}

func validateUpload(upload UploadRequest) error {
	base := filepath.Base(upload.Filename)
	lower := strings.ToLower(base)
	if upload.Open == nil || base != upload.Filename || strings.Contains(upload.Filename, "\\") || hasControl(upload.Filename) || upload.Size < 0 || upload.Size > maximumUploadSize || (!strings.HasSuffix(lower, ".nzb") && !strings.HasSuffix(lower, ".nzb.gz")) {
		return ErrInvalidUpload
	}
	if upload.Priority != PriorityHigh && upload.Priority != PriorityNormal && upload.Priority != PriorityLow {
		return ErrInvalidUpload
	}
	if upload.RelativePath != "" {
		if _, err := library.ValidateRelative(upload.RelativePath); err != nil {
			return ErrInvalidUpload
		}
	}
	return nil
}

func hasControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func encodeUpload(upload UploadRequest) ([]byte, string, error) {
	reader, err := upload.Open()
	if err != nil {
		return nil, "", err
	}
	defer reader.Close()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", upload.Filename)
	if err != nil {
		return nil, "", err
	}
	written, err := io.Copy(part, io.LimitReader(reader, maximumUploadSize+1))
	if err != nil {
		return nil, "", err
	}
	if written > maximumUploadSize || upload.Size > 0 && written != upload.Size {
		return nil, "", ErrInvalidUpload
	}
	if upload.Category != "" {
		_ = writer.WriteField("category", upload.Category)
	}
	if upload.RelativePath != "" {
		_ = writer.WriteField("relative_path", upload.RelativePath)
	}
	_ = writer.WriteField("priority", strconv.Itoa(int(upload.Priority)))
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}
