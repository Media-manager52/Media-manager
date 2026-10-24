package backups

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	s3BucketPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	s3PayloadPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

const s3EmptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

type s3Store struct {
	base      *url.URL
	region    string
	accessKey string
	secretKey string
	prefix    string
	client    *http.Client
	now       func() time.Time
}

func newS3Store(config S3Config) (*s3Store, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("off-site S3 endpoint is invalid")
	}
	if parsed.Scheme != "https" && !(config.AllowHTTP && parsed.Scheme == "http") {
		return nil, errors.New("off-site S3 endpoint must use HTTPS")
	}
	bucket := strings.TrimSpace(config.Bucket)
	if !s3BucketPattern.MatchString(bucket) {
		return nil, errors.New("off-site S3 bucket is invalid")
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		return nil, errors.New("off-site S3 region is required")
	}
	accessKey, secretKey := strings.TrimSpace(config.AccessKey), strings.TrimSpace(config.SecretKey)
	if accessKey == "" || secretKey == "" {
		return nil, errors.New("off-site S3 access key and secret key are required")
	}
	base := *parsed
	base.Path = strings.TrimRight(parsed.Path, "/") + "/" + bucket
	return &s3Store{base: &base, region: region, accessKey: accessKey, secretKey: secretKey, client: offsiteHTTPClient(config.Client, 15*time.Minute), now: time.Now}, nil
}

func (store *s3Store) objectURL(key string) string {
	copied := *store.base
	copied.Path = strings.TrimRight(store.base.Path, "/") + "/" + key
	return copied.String()
}

func (store *s3Store) Put(ctx context.Context, key string, source io.Reader, size int64, sha256Hex string) error {
	if size < 1 {
		return errors.New("off-site backup size is invalid")
	}
	if !s3PayloadPattern.MatchString(sha256Hex) {
		return errors.New("off-site backup checksum is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, store.objectURL(key), source)
	if err != nil {
		return err
	}
	request.ContentLength = size
	request.Header.Set("Content-Type", "application/octet-stream")
	store.sign(request, sha256Hex, store.now().UTC())
	response, err := store.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return offsiteResponseError("S3 upload", response)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return nil
}

func (store *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, store.objectURL(key), nil)
	if err != nil {
		return nil, err
	}
	store.sign(request, s3EmptyPayloadSHA256, store.now().UTC())
	response, err := store.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return nil, ErrOffsiteObjectNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, offsiteResponseError("S3 download", response)
	}
	return response.Body, nil
}

func (store *s3Store) sign(request *http.Request, payloadHash string, now time.Time) {
	date := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	request.Header.Set("x-amz-content-sha256", payloadHash)
	request.Header.Set("x-amz-date", amzDate)
	canonicalURI := request.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalHeaders := "host:" + request.URL.Host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{
		request.Method,
		canonicalURI,
		"",
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := date + "/" + store.region + "/s3/aws4_request"
	digest := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hex.EncodeToString(digest[:]),
	}, "\n")
	key := hmacSHA256([]byte("AWS4"+store.secretKey), date)
	key = hmacSHA256(key, store.region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))
	request.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", store.accessKey, scope, signedHeaders, signature))
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func offsiteResponseError(operation string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		return fmt.Errorf("%s returned HTTP %d", operation, response.StatusCode)
	}
	return fmt.Errorf("%s returned HTTP %d: %s", operation, response.StatusCode, detail)
}
