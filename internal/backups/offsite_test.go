package backups

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func TestNewOffsiteValidatesConfiguration(t *testing.T) {
	if store, kind, err := NewOffsite(OffsiteConfig{}); store != nil || kind != "" || err != nil {
		t.Fatalf("empty kind store=%v kind=%q err=%v", store, kind, err)
	}
	if _, _, err := NewOffsite(OffsiteConfig{Kind: "ftp"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, _, err := NewOffsite(OffsiteConfig{Kind: "s3", S3: S3Config{Endpoint: "http://example.test", Region: "r", Bucket: "b", AccessKey: "a", SecretKey: "s"}}); err == nil {
		t.Fatal("HTTP S3 endpoint accepted without AllowHTTP")
	}
	if _, _, err := NewOffsite(OffsiteConfig{Kind: "webdav", WebDAV: WebDAVConfig{URL: "http://example.test"}}); err == nil {
		t.Fatal("HTTP WebDAV URL accepted without AllowHTTP")
	}
	if _, _, err := NewOffsite(OffsiteConfig{Kind: "s3", Prefix: "../escape", S3: S3Config{Endpoint: "https://example.test", Region: "r", Bucket: "b", AccessKey: "a", SecretKey: "s"}}); err == nil {
		t.Fatal("parent traversal prefix accepted")
	}
	if _, _, err := NewOffsite(OffsiteConfig{Kind: "s3", Prefix: "with space", S3: S3Config{Endpoint: "https://example.test", Region: "r", Bucket: "b", AccessKey: "a", SecretKey: "s"}}); err == nil {
		t.Fatal("unsafe prefix accepted")
	}
}

func TestOffsiteKey(t *testing.T) {
	key, err := OffsiteKey("/media-manager/backups/", "01a1.mmbackup")
	if err != nil || key != "media-manager/backups/01a1.mmbackup" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	key, err = OffsiteKey("", "01a1.mmbackup")
	if err != nil || key != "01a1.mmbackup" {
		t.Fatalf("root key=%q err=%v", key, err)
	}
	for _, filename := range []string{"", "../x", "dir/x", `dir\x`} {
		if _, err := OffsiteKey("prefix", filename); err == nil {
			t.Fatalf("invalid filename %q accepted", filename)
		}
	}
}

func TestValidOffsiteKey(t *testing.T) {
	for _, key := range []string{"01a1.mmbackup", "media-manager/backups/01a1.mmbackup"} {
		if !ValidOffsiteKey(key) {
			t.Fatalf("valid key %q rejected", key)
		}
	}
	for _, key := range []string{"", "/leading", "../x", "prefix/../x", `dir\\x`, "with space/x"} {
		if ValidOffsiteKey(key) {
			t.Fatalf("unsafe key %q accepted", key)
		}
	}
}

func TestS3PutAndGet(t *testing.T) {
	var putPath, putAuthorization, putDate, putPayloadHash, putBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(request.Body)
			putPath = request.URL.Path
			putAuthorization = request.Header.Get("Authorization")
			putDate = request.Header.Get("x-amz-date")
			putPayloadHash = request.Header.Get("x-amz-content-sha256")
			putBody = string(body)
			writer.WriteHeader(http.StatusOK)
		case http.MethodGet:
			_, _ = writer.Write([]byte("encrypted backup"))
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	store, kind, err := NewOffsite(OffsiteConfig{
		Kind:   "s3",
		Prefix: "media-manager/backups",
		S3: S3Config{
			Endpoint: server.URL, Region: "eu-test-1", Bucket: "backups",
			AccessKey: "access", SecretKey: "secret", AllowHTTP: true,
		},
	})
	if err != nil || kind != "s3" {
		t.Fatalf("store=%v kind=%q err=%v", store, kind, err)
	}
	key, _ := OffsiteKey("media-manager/backups", "01a1.mmbackup")
	if err := store.Put(t.Context(), key, strings.NewReader("encrypted backup"), int64(len("encrypted backup")), testSHA256("encrypted backup")); err != nil {
		t.Fatal(err)
	}
	if putPath != "/backups/media-manager/backups/01a1.mmbackup" {
		t.Fatalf("put path=%q", putPath)
	}
	if !strings.HasPrefix(putAuthorization, "AWS4-HMAC-SHA256 Credential=access/") || !strings.Contains(putAuthorization, "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
		t.Fatalf("authorization=%q", putAuthorization)
	}
	if putDate == "" || putPayloadHash != testSHA256("encrypted backup") || putBody != "encrypted backup" {
		t.Fatalf("date=%q payload=%q body=%q", putDate, putPayloadHash, putBody)
	}
	body, err := store.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	read, _ := io.ReadAll(body)
	if string(read) != "encrypted backup" {
		t.Fatalf("get body=%q", read)
	}
}

func TestS3GetMissingObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	store, _, err := NewOffsite(OffsiteConfig{Kind: "s3", S3: S3Config{Endpoint: server.URL, Region: "r", Bucket: "b", AccessKey: "a", SecretKey: "s", AllowHTTP: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), "missing.mmbackup"); err != ErrOffsiteObjectNotFound {
		t.Fatalf("err=%v", err)
	}
}

func TestWebDAVPutCreatesCollectionsAndGets(t *testing.T) {
	var collections []string
	var putPath, putAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case "MKCOL":
			collections = append(collections, request.URL.Path)
			writer.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			putPath = request.URL.Path
			putAuthorization = request.Header.Get("Authorization")
			writer.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			_, _ = writer.Write([]byte("encrypted backup"))
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	store, kind, err := NewOffsite(OffsiteConfig{
		Kind:   "webdav",
		Prefix: "media-manager/backups",
		WebDAV: WebDAVConfig{URL: server.URL + "/dav", Username: "owner", Password: "secret", AllowHTTP: true},
	})
	if err != nil || kind != "webdav" {
		t.Fatalf("store=%v kind=%q err=%v", store, kind, err)
	}
	key, _ := OffsiteKey("media-manager/backups", "01a1.mmbackup")
	payload := "encrypted backup"
	if err := store.Put(t.Context(), key, strings.NewReader(payload), int64(len(payload)), testSHA256(payload)); err != nil {
		t.Fatal(err)
	}
	if len(collections) != 2 || collections[0] != "/dav/media-manager" || collections[1] != "/dav/media-manager/backups" {
		t.Fatalf("collections=%v", collections)
	}
	if putPath != "/dav/media-manager/backups/01a1.mmbackup" {
		t.Fatalf("put path=%q", putPath)
	}
	if !strings.HasPrefix(putAuthorization, "Basic ") {
		t.Fatalf("authorization=%q", putAuthorization)
	}
	body, err := store.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	read, _ := io.ReadAll(body)
	if string(read) != payload {
		t.Fatalf("get body=%q", read)
	}
}

func TestWebDAVGetMissingObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	store, _, err := NewOffsite(OffsiteConfig{Kind: "webdav", WebDAV: WebDAVConfig{URL: server.URL, AllowHTTP: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), "missing.mmbackup"); err != ErrOffsiteObjectNotFound {
		t.Fatalf("err=%v", err)
	}
}
